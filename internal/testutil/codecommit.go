package testutil

import (
	"bytes"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // SNS SignatureVersion 1
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// CodeCommitPR is a pull request the fake recorded.
type CodeCommitPR struct {
	Repo, Head, Base, Title, Description, Token string
	ID                                          int
	// Draft is derived from the title the way ripple writes it: a leading "Draft:".
	Draft bool
}

// CodeCommit is a fake AWS CodeCommit for one region: the JSON 1.1 API pieces ripple
// uses, answered only to requests whose SigV4 signature it recomputes itself (not with
// the code under test); git smart-HTTP over the same bare repositories, answered only
// to a git password it recomputes the way git-remote-codecommit does; and the SNS side
// (a signing key, a certificate endpoint and a subscription confirmation endpoint) for
// receiver tests. Repo paths are "<region>/<name>".
//
// The signature check ignores the clock: the conformance suite runs on a fixed one.
type CodeCommit struct {
	Region string
	// APIURL is the API endpoint (Client.Endpoint); GitURL the git endpoint (Client.GitBase).
	APIURL, GitURL string
	// CertURL is where the SNS signing certificate is served.
	CertURL string
	// AccessKey, SecretKey and SessionToken are the one credential the fake accepts.
	AccessKey, SecretKey, SessionToken string

	t    *testing.T
	root string
	key  *rsa.PrivateKey

	mu        sync.Mutex
	members   map[string]bool
	calls     map[string]int
	prs       map[string][]*CodeCommitPR
	limitNext bool
	requests  []string
	apiHost   string
	confirmed int
	msgSeq    int
	gitSeen   int
	gitDenied int
}

func NewCodeCommit(t *testing.T, region string) *CodeCommit {
	t.Helper()
	c := &CodeCommit{Region: region, t: t, root: t.TempDir(), members: map[string]bool{}, calls: map[string]int{},
		prs: map[string][]*CodeCommitPR{}, AccessKey: "AKIDEXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		SessionToken: "FwoGZXIvYXdzEJr//session+token/with=chars"}
	var err error
	if c.key, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(c.serveAPI))
	t.Cleanup(api.Close)
	c.APIURL = api.URL
	c.apiHost = strings.TrimPrefix(api.URL, "http://")
	c.CertURL = api.URL + "/cert.pem"
	backend, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skipf("git not available: %v", err)
	}
	h := &cgi.Handler{Path: strings.TrimSpace(string(backend)) + "/git-http-backend",
		Env: []string{"GIT_PROJECT_ROOT=" + c.root, "GIT_HTTP_EXPORT_ALL=1",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.receivepack", "GIT_CONFIG_VALUE_0=true"}}
	gs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { c.serveGit(h, w, r) }))
	t.Cleanup(gs.Close)
	c.GitURL = gs.URL
	return c
}

// Member makes path appear in the repository listing.
func (c *CodeCommit) Member(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.members[path] = true
}

// RateLimitNext answers the next CreatePullRequest with a ThrottlingException.
func (c *CodeCommit) RateLimitNext() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.limitNext = true
}

func (c *CodeCommit) name(path string) string {
	region, name, ok := strings.Cut(path, "/")
	if !ok || region != c.Region {
		c.t.Fatalf("fake codecommit: path %q is not in region %s", path, c.Region)
	}
	return name
}

func (c *CodeCommit) bare(name string) string { return filepath.Join(c.root, name+".git") }

// AddRepo creates a repository whose main branch holds files.
func (c *CodeCommit) AddRepo(path string, files map[string]string) {
	c.t.Helper()
	bare := c.bare(c.name(path))
	if err := os.MkdirAll(bare, 0o755); err != nil {
		c.t.Fatal(err)
	}
	git(c.t, bare, "init", "--bare", "-b", "main")
	git(c.t, bare, "config", "uploadpack.allowFilter", "true")
	c.Commit(path, files)
}

// Commit adds a commit on main with files (a human pushing to the default branch).
func (c *CodeCommit) Commit(path string, files map[string]string) {
	c.t.Helper()
	work := c.t.TempDir()
	git(c.t, work, "clone", "-q", c.bare(c.name(path)), "w")
	w := filepath.Join(work, "w")
	git(c.t, w, "checkout", "-q", "-B", "main")
	for p, content := range files {
		full := filepath.Join(w, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			c.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			c.t.Fatal(err)
		}
	}
	git(c.t, w, "add", "-A")
	git(c.t, w, "commit", "-q", "-m", "test commit")
	git(c.t, w, "push", "-q", "origin", "main")
}

// FileAt returns the content of file on branch, or ok=false when absent.
func (c *CodeCommit) FileAt(path, branch, file string) (string, bool) {
	cmd := exec.Command("git", "show", branch+":"+file)
	cmd.Dir = c.bare(c.name(path))
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func (c *CodeCommit) hasBranch(name, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = c.bare(name)
	cmd.Env = gitEnv()
	return cmd.Run() == nil
}

// Calls is the number of repository-addressed API requests for path.
func (c *CodeCommit) Calls(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[c.name(path)]
}

// PRs lists the pull requests opened in path.
func (c *CodeCommit) PRs(path string) []CodeCommitPR {
	name := c.name(path)
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []CodeCommitPR
	for _, p := range c.prs[name] {
		out = append(out, *p)
	}
	return out
}

// Requests is "X-Amz-Target" of every API request so far, in order.
func (c *CodeCommit) Requests() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.requests...)
}

// Confirmed is how many subscription confirmations were visited.
func (c *CodeCommit) Confirmed() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.confirmed
}

// GitDenied is how many git requests carried a wrong or missing git password.
func (c *CodeCommit) GitDenied() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gitDenied
}

func ccHMAC(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

func ccSigKey(secret, date, region string) []byte {
	k := ccHMAC([]byte("AWS4"+secret), date)
	k = ccHMAC(k, region)
	k = ccHMAC(k, "codecommit")
	return ccHMAC(k, "aws4_request")
}

func (c *CodeCommit) awsErr(w http.ResponseWriter, code int, typ, msg string) {
	c.t.Logf("fake codecommit: %d %s %s", code, typ, msg)
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.Header().Set("x-amzn-ErrorType", typ)
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"__type": typ, "message": msg})
}

// verifySigV4 recomputes the Authorization header's signature from the request alone,
// following docs/vendor/codecommit/iam-reference_sigv-create-signed-request.md.
func (c *CodeCommit) verifySigV4(r *http.Request, body []byte) string {
	a, ok := strings.CutPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ")
	if !ok {
		return "missing or wrong Authorization algorithm"
	}
	f := map[string]string{}
	for _, part := range strings.Split(a, ", ") {
		k, v, _ := strings.Cut(part, "=")
		f[k] = v
	}
	cred := strings.Split(f["Credential"], "/")
	if len(cred) != 5 || cred[0] != c.AccessKey || cred[2] != c.Region || cred[3] != "codecommit" || cred[4] != "aws4_request" {
		return "bad credential scope " + f["Credential"]
	}
	if r.Header.Get("X-Amz-Security-Token") != c.SessionToken {
		return "bad session token"
	}
	date := r.Header.Get("X-Amz-Date")
	if len(date) != 16 || date[:8] != cred[1] {
		return "bad X-Amz-Date"
	}
	var canon strings.Builder
	signed := strings.Split(f["SignedHeaders"], ";")
	for _, h := range signed {
		v := r.Host
		if h != "host" {
			v = strings.Join(strings.Fields(strings.Join(r.Header.Values(h), ",")), " ")
		}
		canon.WriteString(h + ":" + v + "\n")
	}
	for _, need := range []string{"host", "x-amz-date", "x-amz-target"} {
		if !ccContains(signed, need) {
			return "header " + need + " not signed"
		}
	}
	sum := sha256.Sum256(body)
	creq := r.Method + "\n" + r.URL.EscapedPath() + "\n" + r.URL.RawQuery + "\n" + canon.String() + "\n" + f["SignedHeaders"] + "\n" + hex.EncodeToString(sum[:])
	h := sha256.Sum256([]byte(creq))
	sts := "AWS4-HMAC-SHA256\n" + date + "\n" + strings.Join(cred[1:], "/") + "\n" + hex.EncodeToString(h[:])
	if hex.EncodeToString(ccHMAC(ccSigKey(c.SecretKey, cred[1], c.Region), sts)) != f["Signature"] {
		return "signature mismatch"
	}
	return ""
}

func ccContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func (c *CodeCommit) serveAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/cert.pem" {
		der, err := c.certDER()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = pem.Encode(w, &pem.Block{Type: "CERTIFICATE", Bytes: der})
		return
	}
	if r.URL.Path == "/confirm" {
		c.mu.Lock()
		c.confirmed++
		c.mu.Unlock()
		_, _ = io.WriteString(w, "<ConfirmSubscriptionResponse/>")
		return
	}
	body, _ := io.ReadAll(r.Body)
	op, _ := strings.CutPrefix(r.Header.Get("X-Amz-Target"), "CodeCommit_20150413.")
	c.mu.Lock()
	c.requests = append(c.requests, op)
	c.mu.Unlock()
	if r.Method != http.MethodPost || r.URL.Path != "/" || r.Header.Get("Content-Type") != "application/x-amz-json-1.1" {
		c.awsErr(w, http.StatusBadRequest, "InvalidRequest", "not a JSON 1.1 POST to /")
		return
	}
	if why := c.verifySigV4(r, body); why != "" {
		c.awsErr(w, http.StatusForbidden, "InvalidSignatureException", why)
		return
	}
	var in map[string]any
	if err := json.Unmarshal(body, &in); err != nil {
		c.awsErr(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	switch op {
	case "ListRepositories":
		c.listRepositories(w, in)
	case "ListPullRequests":
		c.listPullRequests(w, in)
	case "GetPullRequest":
		c.getPullRequest(w, in)
	case "GetDifferences":
		c.getDifferences(w, in)
	case "CreatePullRequest":
		c.createPullRequest(w, in)
	default:
		c.awsErr(w, http.StatusBadRequest, "UnknownOperationException", op)
	}
}

// ccPageSize is small so listing tests cross page boundaries.
const ccPageSize = 2

func ccPage(all []string, in map[string]any, tokenKey string) (items []string, next string) {
	start := 0
	if tok, _ := in[tokenKey].(string); tok != "" {
		start, _ = strconv.Atoi(tok)
	}
	end := start + ccPageSize
	if end < len(all) {
		next = strconv.Itoa(end)
	} else {
		end = len(all)
	}
	if start > end {
		start = end
	}
	return all[start:end], next
}

func (c *CodeCommit) listRepositories(w http.ResponseWriter, in map[string]any) {
	c.mu.Lock()
	var all []string
	for p := range c.members {
		all = append(all, strings.TrimPrefix(p, c.Region+"/"))
	}
	c.mu.Unlock()
	sort.Strings(all)
	items, next := ccPage(all, in, "nextToken")
	repos := []map[string]string{}
	for _, n := range items {
		repos = append(repos, map[string]string{"repositoryName": n, "repositoryId": "id-" + n})
	}
	out := map[string]any{"repositories": repos}
	if next != "" {
		out["nextToken"] = next
	}
	_ = json.NewEncoder(w).Encode(out)
}

// repo resolves the repositoryName of a request, counting the call.
func (c *CodeCommit) repo(w http.ResponseWriter, name string) bool {
	if name == "" {
		c.awsErr(w, http.StatusBadRequest, "RepositoryNameRequiredException", "repositoryName")
		return false
	}
	if _, err := os.Stat(c.bare(name)); err != nil || strings.Contains(name, "/") {
		c.awsErr(w, http.StatusBadRequest, "RepositoryDoesNotExistException", name)
		return false
	}
	c.mu.Lock()
	c.calls[name]++
	c.mu.Unlock()
	return true
}

func (c *CodeCommit) listPullRequests(w http.ResponseWriter, in map[string]any) {
	name, _ := in["repositoryName"].(string)
	if !c.repo(w, name) {
		return
	}
	if in["pullRequestStatus"] != "OPEN" {
		c.awsErr(w, http.StatusBadRequest, "InvalidPullRequestStatusException", "OPEN required")
		return
	}
	c.mu.Lock()
	var ids []string
	for _, p := range c.prs[name] {
		ids = append(ids, strconv.Itoa(p.ID))
	}
	c.mu.Unlock()
	items, next := ccPage(ids, in, "nextToken")
	if items == nil {
		items = []string{}
	}
	out := map[string]any{"pullRequestIds": items}
	if next != "" {
		out["nextToken"] = next
	}
	_ = json.NewEncoder(w).Encode(out)
}

func ccPRJSON(name string, p *CodeCommitPR) map[string]any {
	return map[string]any{"pullRequestId": strconv.Itoa(p.ID), "title": p.Title, "description": p.Description,
		"pullRequestStatus": "OPEN", "clientRequestToken": p.Token,
		"pullRequestTargets": []map[string]any{{"repositoryName": name,
			"sourceReference": "refs/heads/" + p.Head, "destinationReference": "refs/heads/" + p.Base}}}
}

func (c *CodeCommit) getPullRequest(w http.ResponseWriter, in map[string]any) {
	id, _ := in["pullRequestId"].(string)
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, list := range c.prs {
		for _, p := range list {
			if strconv.Itoa(p.ID) == id && p.Repo == name {
				c.calls[name]++
				_ = json.NewEncoder(w).Encode(map[string]any{"pullRequest": ccPRJSON(name, p)})
				return
			}
		}
	}
	c.awsErr(w, http.StatusBadRequest, "PullRequestDoesNotExistException", id)
}

func (c *CodeCommit) createPullRequest(w http.ResponseWriter, in map[string]any) {
	targets, _ := in["targets"].([]any)
	if len(targets) != 1 {
		c.awsErr(w, http.StatusBadRequest, "TargetRequiredException", "one target")
		return
	}
	tg, _ := targets[0].(map[string]any)
	name, _ := tg["repositoryName"].(string)
	if !c.repo(w, name) {
		return
	}
	c.mu.Lock()
	if c.limitNext {
		c.limitNext = false
		c.mu.Unlock()
		c.awsErr(w, http.StatusBadRequest, "ThrottlingException", "Rate exceeded")
		return
	}
	c.mu.Unlock()
	title, _ := in["title"].(string)
	desc, _ := in["description"].(string)
	token, _ := in["clientRequestToken"].(string)
	src, _ := tg["sourceReference"].(string)
	dst, _ := tg["destinationReference"].(string)
	head, ok1 := strings.CutPrefix(src, "refs/heads/")
	base, ok2 := strings.CutPrefix(dst, "refs/heads/")
	if title == "" || utf8.RuneCountInString(title) > 100 || utf8.RuneCountInString(desc) > 10240 || !ok1 || !ok2 {
		c.awsErr(w, http.StatusBadRequest, "InvalidTitleException", "title, description or references")
		return
	}
	for _, b := range []string{head, base} {
		if !c.hasBranch(name, b) {
			c.awsErr(w, http.StatusBadRequest, "ReferenceDoesNotExistException", b)
			return
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A repeated token returns the pull request it created, as the real API does.
	for _, p := range c.prs[name] {
		if token != "" && p.Token == token {
			_ = json.NewEncoder(w).Encode(map[string]any{"pullRequest": ccPRJSON(name, p)})
			return
		}
	}
	p := &CodeCommitPR{Repo: name, Head: head, Base: base, Title: title, Description: desc, Token: token,
		ID: len(c.prs[name]) + 1, Draft: strings.HasPrefix(title, "Draft:")}
	c.prs[name] = append(c.prs[name], p)
	_ = json.NewEncoder(w).Encode(map[string]any{"pullRequest": ccPRJSON(name, p)})
}

// serveGit answers git smart-HTTP at /v1/repos/<name>/..., after recomputing the git
// password from the secret key (docs/vendor/codecommit/grc-init.py sign()).
func (c *CodeCommit) serveGit(h *cgi.Handler, w http.ResponseWriter, r *http.Request) {
	deny := func(why string) {
		c.t.Logf("fake codecommit git: denied: %s", why)
		c.mu.Lock()
		c.gitDenied++
		c.mu.Unlock()
		w.Header().Set("WWW-Authenticate", `Basic realm="CodeCommit"`)
		http.Error(w, "forbidden", http.StatusUnauthorized)
	}
	user, pass, ok := basicAuth(r)
	if !ok {
		deny("no basic auth")
		return
	}
	if want := c.AccessKey + "%" + c.SessionToken; user != want {
		deny("bad user " + user)
		return
	}
	rest, found := strings.CutPrefix(r.URL.Path, "/v1/repos/")
	name, tail, _ := strings.Cut(rest, "/")
	if !found || name == "" || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	ts, sig, ok := strings.Cut(pass, "Z")
	if !ok || len(ts) != 15 {
		deny("password has no timestamp")
		return
	}
	date := ts[:8]
	creq := "GIT\n/v1/repos/" + name + "\n\nhost:" + r.Host + "\n\nhost\n"
	h2 := sha256.Sum256([]byte(creq))
	sts := "AWS4-HMAC-SHA256\n" + ts + "\n" + date + "/" + c.Region + "/codecommit/aws4_request\n" + hex.EncodeToString(h2[:])
	if hex.EncodeToString(ccHMAC(ccSigKey(c.SecretKey, date, c.Region), sts)) != sig {
		deny("signature mismatch")
		return
	}
	if _, err := os.Stat(c.bare(name)); err != nil {
		http.NotFound(w, r)
		return
	}
	if r.ContentLength < 0 {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body, r.ContentLength, r.TransferEncoding = io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
		r.Header.Del("Transfer-Encoding")
	}
	c.mu.Lock()
	c.gitSeen++
	c.mu.Unlock()
	r.URL.Path = "/" + name + ".git/" + tail
	h.ServeHTTP(w, r)
}

func (c *CodeCommit) certDER() ([]byte, error) {
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "sns.example.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	return x509.CreateCertificate(rand.Reader, tpl, tpl, &c.key.PublicKey, c.key)
}

// SNSMessage is a signed SNS HTTPS delivery: its headers and body. typ is
// "Notification" or "SubscriptionConfirmation"; version is the SignatureVersion ("1" or
// "2"); message is the Message (an EventBridge event for a notification).
func (c *CodeCommit) SNSMessage(typ, version, topic, message string) (http.Header, []byte) {
	// Every SNS message has its own MessageId (a retry reuses it), and the receiver dedupes on it.
	c.mu.Lock()
	c.msgSeq++
	id := fmt.Sprintf("165545c9-2a5c-472c-8df2-%012d", c.msgSeq)
	c.mu.Unlock()
	m := map[string]string{"Type": typ, "MessageId": id, "TopicArn": topic,
		"Message": message, "Timestamp": "2026-11-23T10:00:00.751Z", "SignatureVersion": version, "SigningCertURL": c.CertURL}
	var sts string
	if typ == "SubscriptionConfirmation" {
		m["Token"] = "2336412f37fb687f5d51e6e2425"
		m["SubscribeURL"] = c.APIURL + "/confirm?Action=ConfirmSubscription"
		sts = ccSignString(m, "Message", "MessageId", "SubscribeURL", "Timestamp", "Token", "TopicArn", "Type")
	} else {
		sts = ccSignString(m, "Message", "MessageId", "Timestamp", "TopicArn", "Type")
	}
	var sum []byte
	var hash crypto.Hash
	if version == "1" {
		s := sha1.Sum([]byte(sts)) //nolint:gosec
		sum, hash = s[:], crypto.SHA1
	} else {
		s := sha256.Sum256([]byte(sts))
		sum, hash = s[:], crypto.SHA256
	}
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.key, hash, sum)
	if err != nil {
		c.t.Fatal(err)
	}
	m["Signature"] = base64.StdEncoding.EncodeToString(sig)
	body, _ := json.Marshal(m)
	h := http.Header{}
	h.Set("x-amz-sns-message-type", typ)
	h.Set("x-amz-sns-topic-arn", topic)
	h.Set("Content-Type", "text/plain; charset=UTF-8")
	return h, body
}

func ccSignString(m map[string]string, keys ...string) string {
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "\n" + m[k] + "\n")
	}
	return b.String()
}

// Head is the commit main points at in path.
func (c *CodeCommit) Head(path string) string {
	return strings.TrimSpace(git(c.t, c.bare(c.name(path)), "rev-parse", "refs/heads/main"))
}

func (c *CodeCommit) getDifferences(w http.ResponseWriter, in map[string]any) {
	name, _ := in["repositoryName"].(string)
	if !c.repo(w, name) {
		return
	}
	before, _ := in["beforeCommitSpecifier"].(string)
	after, _ := in["afterCommitSpecifier"].(string)
	cmd := exec.Command("git", "diff", "--name-status", "--no-renames", before, after)
	cmd.Dir = c.bare(name)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil || before == "" || after == "" {
		c.awsErr(w, http.StatusBadRequest, "CommitDoesNotExistException", before+".."+after)
		return
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	items, next := ccPage(lines, in, "NextToken")
	diffs := []map[string]any{}
	for _, l := range items {
		kind, p, _ := strings.Cut(l, "\t")
		d := map[string]any{"changeType": kind}
		if kind != "A" {
			d["beforeBlob"] = map[string]string{"path": p, "blobId": "b", "mode": "100644"}
		}
		if kind != "D" {
			d["afterBlob"] = map[string]string{"path": p, "blobId": "a", "mode": "100644"}
		}
		diffs = append(diffs, d)
	}
	resp := map[string]any{"differences": diffs}
	if next != "" {
		resp["NextToken"] = next
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// Branch creates branch in path at main's commit.
func (c *CodeCommit) Branch(path, branch string) {
	c.t.Helper()
	git(c.t, c.bare(c.name(path)), "branch", branch, "main")
}
