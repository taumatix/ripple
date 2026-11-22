package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

// TokenSource yields an installation access token.
type TokenSource interface {
	Token(ctx context.Context, installationID int64) (string, error)
}

// ParsePrivateKey accepts a PKCS#1 or PKCS#8 PEM RSA key.
func ParsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	blk, _ := pem.Decode(pemBytes)
	if blk == nil {
		return nil, errors.New("private key: no PEM block found")
	}
	if k, err := x509.ParsePKCS1PrivateKey(blk.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, errors.New("private key: not a PKCS#1 or PKCS#8 RSA key")
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key: not an RSA key")
	}
	return rk, nil
}

// AppAuth authenticates as a GitHub App (RS256 JWT) and caches installation tokens.
type AppAuth struct {
	AppID   string
	Key     *rsa.PrivateKey
	APIBase string
	HTTP    *http.Client
	Now     func() time.Time

	mu    sync.Mutex
	cache map[int64]cached
}

type cached struct {
	token   string
	expires time.Time
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// JWT returns a signed app JWT valid for 9 minutes (GitHub's maximum is 10).
func (a *AppAuth) JWT() (string, error) {
	now := a.now()
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": a.AppID})
	in := b64(hdr) + "." + b64(claims)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.Key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return in + "." + b64(sig), nil
}

func (a *AppAuth) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *AppAuth) Token(ctx context.Context, id int64) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c, ok := a.cache[id]; ok && c.expires.Sub(a.now()) > 2*time.Minute {
		return c.token, nil
	}
	jwt, err := a.JWT()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/app/installations/%d/access_tokens", strings.TrimRight(a.APIBase, "/"), id), bytes.NewReader(nil))
	if err != nil {
		return "", err
	}
	setHeaders(req)
	req.Header.Set("Authorization", "Bearer "+jwt)
	hc := a.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("installation token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("installation token: status %d", resp.StatusCode)
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Token == "" {
		return "", errors.New("installation token: malformed response")
	}
	if a.cache == nil {
		a.cache = map[int64]cached{}
	}
	a.cache[id] = cached{out.Token, out.ExpiresAt}
	return out.Token, nil
}

// AppCredentials is the platform.Credentials of a TokenSource: the token for the
// repo's installation, sent to git as x-access-token.
type AppCredentials struct{ Tokens TokenSource }

func (a AppCredentials) Credential(ctx context.Context, repo model.Repo) (platform.Credential, error) {
	tok, err := a.Tokens.Token(ctx, repo.InstallationID)
	if err != nil {
		return platform.Credential{}, err
	}
	return platform.Credential{Username: "x-access-token", Secret: tok}, nil
}

// StaticToken is a TokenSource for tests and for personal-access-token use.
type StaticToken string

func (s StaticToken) Token(context.Context, int64) (string, error) { return string(s), nil }
