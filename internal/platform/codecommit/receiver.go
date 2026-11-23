package codecommit

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // SNS SignatureVersion 1 is SHA1-RSA; version 2 is preferred below
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

const (
	// maxBody bounds a delivery. SNS messages are at most 256 KB
	// (docs/vendor/codecommit/sns-sns-message-and-json-formats.md); EventBridge adds an
	// envelope, so a megabyte is far above anything real.
	maxBody = 1 << 20
	// maxInflight bounds how many request bodies are buffered at once.
	maxInflight = 4
	maxCerts    = 8
)

// snsHost is a host SNS signs and confirms from: sns.<region>.amazonaws.com, or its
// China form. docs/vendor/codecommit/sns-sns-verify-signature-of-message-verify-message-signature.md
// requires the SigningCertURL to come from "a trusted AWS domain" and to reject others;
// the same check guards SubscribeURL, so a forged confirmation cannot make ripple fetch
// an arbitrary URL.
var snsHost = regexp.MustCompile(`^sns\.[a-z0-9-]+\.amazonaws\.com(\.cn)?$`)

// AWSURL reports whether u is an https URL on an SNS endpoint host.
func AWSURL(u *url.URL) bool {
	return u.Scheme == "https" && u.Port() == "" && snsHost.MatchString(u.Hostname()) && u.User == nil
}

// Receiver is the http.Handler for POST /webhook/codecommit: an SNS HTTPS subscription
// fed by an EventBridge rule on CodeCommit repository events. Before it reads a body it
// requires the x-amz-sns-topic-arn header to be one of Topics; after, it requires the
// RSA signature SNS puts on every message to verify against the certificate at
// SigningCertURL. With no Topics, every delivery is refused.
type Receiver struct {
	// Topics are the SNS topic ARNs whose messages are accepted.
	Topics []string
	Sink   platform.Sink
	Log    *slog.Logger
	// HTTP fetches signing certificates and confirms subscriptions.
	HTTP *http.Client
	// AllowURL decides which SigningCertURL and SubscribeURL are fetched; nil means AWSURL.
	// A test double on loopback sets it.
	AllowURL func(*url.URL) bool
	// Files looks up the paths a branch update changed between two commits; nil leaves
	// them unknown. NewHost sets it to the client's ChangedFiles.
	Files func(ctx context.Context, repo model.Repo, before, after string) ([]string, error)

	sem   chan struct{} // set by NewHost
	certs *certCache    // set by NewHost
}

// certCache holds a few signing certificates; it is a pointer so a Receiver can be copied.
type certCache struct {
	mu sync.Mutex
	m  map[string]*x509.Certificate
}

func (c *certCache) get(k string) *x509.Certificate {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[k]
}

func (c *certCache) put(k string, v *x509.Certificate) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= maxCerts {
		c.m = map[string]*x509.Certificate{}
	}
	c.m[k] = v
}

func (h *Receiver) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (h *Receiver) client() *http.Client {
	if h.HTTP != nil {
		return h.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (h *Receiver) allow(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}
	ok := AWSURL(u)
	if h.AllowURL != nil {
		ok = h.AllowURL(u)
	}
	return u, ok
}

// message is the JSON an SNS HTTPS endpoint receives
// (sns-http-notification-json.md, sns-http-subscription-confirmation-json.md).
type message struct {
	Type             string
	MessageID        string `json:"MessageId"`
	Token            string
	TopicArn         string
	Subject          *string
	Message          string
	SubscribeURL     string
	Timestamp        string
	SignatureVersion string
	Signature        string
	SigningCertURL   string
}

// stringToSign is the canonical text SNS signs: "Key\nValue\n" pairs in byte order of
// key, only the fields named for the message type, Subject only when present
// (sns-sns-verify-signature-of-message-verify-message-signature.md, steps 6 to 9).
func (m message) stringToSign() (string, bool) {
	var b bytes.Buffer
	add := func(k, v string) { b.WriteString(k + "\n" + v + "\n") }
	switch m.Type {
	case "Notification":
		add("Message", m.Message)
		add("MessageId", m.MessageID)
		if m.Subject != nil {
			add("Subject", *m.Subject)
		}
		add("Timestamp", m.Timestamp)
		add("TopicArn", m.TopicArn)
		add("Type", m.Type)
	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		add("Message", m.Message)
		add("MessageId", m.MessageID)
		add("SubscribeURL", m.SubscribeURL)
		add("Timestamp", m.Timestamp)
		add("Token", m.Token)
		add("TopicArn", m.TopicArn)
		add("Type", m.Type)
	default:
		return "", false
	}
	return b.String(), true
}

// cert returns the signing certificate at u, fetched once and kept.
func (h *Receiver) cert(ctx context.Context, u *url.URL) (*x509.Certificate, error) {
	if c := h.certs.get(u.String()); c != nil {
		return c, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch signing certificate: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch signing certificate: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(data)
	if blk == nil {
		return nil, errors.New("signing certificate is not PEM")
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return nil, err
	}
	h.certs.put(u.String(), c)
	return c, nil
}

// verify checks m's signature. SignatureVersion 1 is SHA1, 2 is SHA256
// (sns-sns-verify-signature-of-message.md).
func (h *Receiver) verify(ctx context.Context, m message) error {
	u, ok := h.allow(m.SigningCertURL)
	if !ok {
		return errors.New("SigningCertURL is not an SNS endpoint")
	}
	sts, ok := m.stringToSign()
	if !ok {
		return errors.New("unknown message type")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return errors.New("signature is not base64")
	}
	var hash crypto.Hash
	var sum []byte
	switch m.SignatureVersion {
	case "1":
		s := sha1.Sum([]byte(sts)) //nolint:gosec
		hash, sum = crypto.SHA1, s[:]
	case "2":
		s := sha256.Sum256([]byte(sts))
		hash, sum = crypto.SHA256, s[:]
	default:
		return fmt.Errorf("unsupported SignatureVersion %q", m.SignatureVersion)
	}
	c, err := h.cert(ctx, u)
	if err != nil {
		return err
	}
	pub, ok := c.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("signing certificate has no RSA key")
	}
	return rsa.VerifyPKCS1v15(pub, hash, sum, sig)
}

// envelope is the EventBridge event SNS carries as Message
// (docs/vendor/codecommit/ug-monitoring-events.md, "referenceUpdated event").
type envelope struct {
	Source     string `json:"source"`
	DetailType string `json:"detail-type"`
	Region     string `json:"region"`
	Detail     struct {
		Event         string `json:"event"`
		Repository    string `json:"repositoryName"`
		ReferenceType string `json:"referenceType"`
		ReferenceFull string `json:"referenceFullName"`
		CommitID      string `json:"commitId"`
		OldCommitID   string `json:"oldCommitId"`
	} `json:"detail"`
}

func (h *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// The topic header is plain and forgeable; it keeps strangers from making us buffer
	// a body and fetch a certificate, and the signed TopicArn is checked again below.
	if !slices.Contains(h.Topics, r.Header.Get("x-amz-sns-topic-arn")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
	default:
		w.Header().Set("Retry-After", "5")
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	// SNS sends Content-Type text/plain, so the body is not decoded by type.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		http.Error(w, "bad body", http.StatusRequestEntityTooLarge)
		return
	}
	var m message
	if err := json.Unmarshal(body, &m); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if m.Type != r.Header.Get("x-amz-sns-message-type") || !slices.Contains(h.Topics, m.TopicArn) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := h.verify(r.Context(), m); err != nil {
		h.log().Warn("codecommit webhook: signature", "err", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch m.Type {
	case "SubscriptionConfirmation":
		h.confirm(w, r.Context(), m)
	case "Notification":
		h.notify(w, r, m)
	default: // UnsubscribeConfirmation: nothing to do.
		w.WriteHeader(http.StatusOK)
	}
}

// confirm visits SubscribeURL, which is what completes a subscription
// (sns-SendMessageToHttp.confirm.md). SNS retries the confirmation if this fails.
func (h *Receiver) confirm(w http.ResponseWriter, ctx context.Context, m message) {
	u, ok := h.allow(m.SubscribeURL)
	if !ok {
		http.Error(w, "bad SubscribeURL", http.StatusBadRequest)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		http.Error(w, "bad SubscribeURL", http.StatusBadRequest)
		return
	}
	resp, err := h.client().Do(req)
	if err != nil {
		h.log().Error("codecommit webhook: confirm subscription", "topic", m.TopicArn, "err", err)
		http.Error(w, "try again", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		h.log().Error("codecommit webhook: confirm subscription", "topic", m.TopicArn, "status", resp.StatusCode)
		http.Error(w, "try again", http.StatusServiceUnavailable)
		return
	}
	h.log().Info("codecommit webhook: subscription confirmed", "topic", m.TopicArn)
	w.WriteHeader(http.StatusOK)
}

// notify turns a branch create or update event into a push. The event names the branch
// and the two commits but not the files, so the files are looked up with one API call;
// when that is impossible or fails, Files stays empty and the repository is scanned in
// full, which costs more but misses nothing.
func (h *Receiver) notify(w http.ResponseWriter, r *http.Request, m message) {
	var e envelope
	if err := json.Unmarshal([]byte(m.Message), &e); err != nil {
		http.Error(w, "bad event", http.StatusBadRequest)
		return
	}
	d := e.Detail
	if e.Source != "aws.codecommit" || e.DetailType != "CodeCommit Repository State Change" ||
		(d.Event != "referenceCreated" && d.Event != "referenceUpdated") || d.ReferenceType != "branch" {
		w.WriteHeader(http.StatusAccepted) // another event on the rule: not an error
		return
	}
	path := e.Region + "/" + d.Repository
	if _, _, err := CheckPath(path); err != nil {
		http.Error(w, "bad repository", http.StatusBadRequest)
		return
	}
	ev := platform.Event{Kind: platform.Push, Delivery: m.MessageID, Repos: []string{path}, Ref: d.ReferenceFull}
	if h.Files != nil && d.Event == "referenceUpdated" && d.OldCommitID != "" && d.CommitID != "" {
		files, err := h.Files(r.Context(), model.Repo{FullName: path}, d.OldCommitID, d.CommitID)
		if err != nil {
			h.log().Warn("codecommit webhook: changed files unknown; scanning in full", "repo", path, "err", err)
		}
		ev.Files = files
	}
	if err := h.Sink.HandleEvent(r.Context(), ev); err != nil {
		h.log().Error("codecommit webhook: sink", "repo", path, "err", err)
		http.Error(w, fmt.Sprintf("try again: %v", err), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
