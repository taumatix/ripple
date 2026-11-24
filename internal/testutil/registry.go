package testutil

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Registry is a fake container registry speaking the Docker Registry HTTP API v2
// (tags list with n/last pagination and a Link header, HEAD and GET of manifests
// with Docker-Content-Digest) behind the anonymous bearer-token flow: every /v2/
// request without the token it hands out is answered 401 with a Bearer challenge
// pointing at its own /token endpoint. It is written from docs/vendor/registry.
// One fake serves every registry host a test names; see registry.Client.BaseURL.
type Registry struct {
	Throttle
	URL string
	srv *httptest.Server

	mu       sync.Mutex
	repos    map[string]map[string]string // repo -> tag -> manifest digest
	pageSize int                          // tags per page; 0 serves all in one
	denied   map[string]bool              // repos whose token request is refused
	created  map[string]time.Time         // "repo:tag" -> image config creation time
	logins   map[string][2]string         // repo -> user, password the token endpoint demands
	identity map[string]string            // repo -> refresh token the token endpoint demands via POST
	basic    *[2]string                   // when set, /v2/ wants HTTP Basic instead of a token
	tokenReq []string                     // "scope basic=true|false" per token request
	reqs     []string
	pushed   map[string]int // "repo:tag" -> push sequence, the fake hub's last_updated
	pushSeq  int
}

const registryToken = "anon-token"

func NewRegistry(t testing.TB) *Registry {
	t.Helper()
	r := &Registry{repos: map[string]map[string]string{}, denied: map[string]bool{}, created: map[string]time.Time{}, logins: map[string][2]string{}, identity: map[string]string{}, pushed: map[string]int{}}
	r.srv = httptest.NewServer(r.Wrap(http.HandlerFunc(r.serve)))
	r.URL = r.srv.URL
	t.Cleanup(r.srv.Close)
	return r
}

// AddTag publishes tag in repo ("library/nginx", "org/img") and returns its manifest
// digest. A tag added twice moves to a new digest, like a rebuilt floating tag.
func (r *Registry) AddTag(repo, tag string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.repos[repo] == nil {
		r.repos[repo] = map[string]string{}
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", repo, tag, len(r.reqs)+len(r.repos[repo]))))
	d := "sha256:" + hex.EncodeToString(sum[:])
	r.repos[repo][tag] = d
	r.pushSeq++
	r.pushed[repo+":"+tag] = r.pushSeq
	return d
}

// AddTagCreated is AddTag for an image whose config says it was created at t. A GET of
// its manifest answers an OCI index (an attestation entry first, then linux/amd64), the
// platform manifest by digest, and the config blob, like a multi-platform image.
func (r *Registry) AddTagCreated(repo, tag string, t time.Time) string {
	d := r.AddTag(repo, tag)
	r.mu.Lock()
	r.created[repo+":"+tag] = t
	r.mu.Unlock()
	return d
}

func derived(kind, digest string) string {
	sum := sha256.Sum256([]byte(kind + ":" + digest))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// serveImage answers GETs of an image's index, platform manifest and config blob
// for a tag added with AddTagCreated; it reports false for any other reference.
func (r *Registry) serveImage(w http.ResponseWriter, repo, ref string, blob bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for tag, d := range r.repos[repo] {
		created, ok := r.created[repo+":"+tag]
		if !ok {
			continue
		}
		plat, cfg := derived("plat", d), derived("cfg", d)
		switch {
		case !blob && ref == tag:
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			w.Header().Set("Docker-Content-Digest", d)
			json.NewEncoder(w).Encode(map[string]any{"schemaVersion": 2, "manifests": []any{
				map[string]any{"digest": derived("att", d), "platform": map[string]string{"os": "unknown", "architecture": "unknown"}},
				map[string]any{"digest": plat, "platform": map[string]string{"os": "linux", "architecture": "amd64"}},
			}})
			return true
		case !blob && ref == plat:
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			json.NewEncoder(w).Encode(map[string]any{"schemaVersion": 2, "config": map[string]any{"digest": cfg}})
			return true
		case blob && ref == cfg:
			json.NewEncoder(w).Encode(map[string]any{"architecture": "amd64", "created": created.UTC().Format(time.RFC3339Nano)})
			return true
		}
	}
	return false
}

// PageSize makes tags/list answer at most n tags per page with a Link header.
func (r *Registry) PageSize(n int) { r.mu.Lock(); r.pageSize = n; r.mu.Unlock() }

// DenyToken refuses the anonymous token for repo (a private image).
func (r *Registry) DenyToken(repo string) { r.mu.Lock(); r.denied[repo] = true; r.mu.Unlock() }

// RequireLogin makes the token endpoint answer 401 for repo unless the request carries
// HTTP Basic credentials for user and password (a private image on a token registry).
func (r *Registry) RequireLogin(repo, user, pass string) {
	r.mu.Lock()
	r.logins[repo] = [2]string{user, pass}
	r.mu.Unlock()
}

// RequireIdentityToken makes the token endpoint answer 401 for repo unless the request is
// the OAuth2 refresh-token grant (POST, grant_type=refresh_token, refresh_token=token, the
// challenge's service and scope, a client_id). A GET with Basic credentials does not do.
func (r *Registry) RequireIdentityToken(repo, token string) {
	r.mu.Lock()
	r.identity[repo] = token
	r.mu.Unlock()
}

// RequireBasic makes every /v2/ request need HTTP Basic credentials for user and
// password, challenged with `WWW-Authenticate: Basic`, like a registry without a token
// service.
func (r *Registry) RequireBasic(user, pass string) {
	r.mu.Lock()
	r.basic = &[2]string{user, pass}
	r.mu.Unlock()
}

// TokenRequests lists one line per token request: its scope and whether it carried
// Basic credentials ("repository:org/img:pull basic=true"). Credentials themselves are
// never recorded.
func (r *Registry) TokenRequests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.tokenReq...)
}

// Requests lists "METHOD path" of every request seen, token requests included.
func (r *Registry) Requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.reqs...)
}

func (r *Registry) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req.Method+" "+req.URL.Path)
	r.mu.Unlock()
	if req.URL.Path == "/token" {
		scope := req.URL.Query().Get("scope") // repository:<repo>:pull
		grant, refresh := "", ""
		if req.Method == http.MethodPost {
			req.ParseForm()
			scope, grant, refresh = req.PostForm.Get("scope"), req.PostForm.Get("grant_type"), req.PostForm.Get("refresh_token")
			if req.PostForm.Get("service") != "fake.registry" || req.PostForm.Get("client_id") == "" {
				grant = "" // a malformed grant is refused below
			}
		}
		parts := strings.Split(scope, ":")
		u, p, hasBasic := req.BasicAuth()
		r.mu.Lock()
		line := fmt.Sprintf("%s basic=%v", scope, hasBasic)
		if req.Method == http.MethodPost {
			line += fmt.Sprintf(" grant=%v", grant == "refresh_token")
		}
		r.tokenReq = append(r.tokenReq, line)
		deny := len(parts) == 3 && r.denied[parts[1]]
		if want, ok := r.identity[strings.Join(parts[1:len(parts)-1], ":")]; ok && len(parts) == 3 {
			deny = deny || grant != "refresh_token" || refresh != want
		}
		if want, ok := r.logins[strings.Join(parts[1:len(parts)-1], ":")]; ok && len(parts) == 3 {
			deny = deny || !hasBasic || u != want[0] || p != want[1]
		}
		r.mu.Unlock()
		if deny {
			http.Error(w, `{"errors":[{"code":"DENIED"}]}`, http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"token": registryToken, "expires_in": 300})
		return
	}
	rest, ok := strings.CutPrefix(req.URL.Path, "/v2/")
	if !ok {
		http.NotFound(w, req)
		return
	}
	if hub, ok := strings.CutPrefix(rest, "namespaces/"); ok {
		r.serveHubTags(w, req, hub)
		return
	}
	i := strings.LastIndex(strings.TrimSuffix(rest, "/"), "/tags/")
	j := strings.LastIndex(rest, "/manifests/")
	k := strings.LastIndex(rest, "/blobs/")
	var repo string
	switch {
	case i >= 0 && strings.HasSuffix(rest, "/tags/list"):
		repo = rest[:i]
	case j >= 0:
		repo = rest[:j]
	case k >= 0:
		repo = rest[:k]
	default:
		http.NotFound(w, req)
		return
	}
	r.mu.Lock()
	basic := r.basic
	r.mu.Unlock()
	if basic != nil {
		if u, p, ok := req.BasicAuth(); !ok || u != basic[0] || p != basic[1] {
			w.Header().Set("WWW-Authenticate", `Basic realm="fake.registry"`)
			http.Error(w, `{"errors":[{"code":"UNAUTHORIZED"}]}`, http.StatusUnauthorized)
			return
		}
	} else if req.Header.Get("Authorization") != "Bearer "+registryToken {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="fake.registry",scope="repository:%s:pull"`, r.URL, repo))
		http.Error(w, `{"errors":[{"code":"UNAUTHORIZED"}]}`, http.StatusUnauthorized)
		return
	}
	r.mu.Lock()
	tags, found := r.repos[repo]
	size := r.pageSize
	r.mu.Unlock()
	if !found {
		http.Error(w, `{"errors":[{"code":"NAME_UNKNOWN"}]}`, http.StatusNotFound)
		return
	}
	if k >= 0 {
		if !r.serveImage(w, repo, rest[k+len("/blobs/"):], true) {
			http.Error(w, `{"errors":[{"code":"BLOB_UNKNOWN"}]}`, http.StatusNotFound)
		}
		return
	}
	if j >= 0 {
		ref := rest[j+len("/manifests/"):]
		if req.Method == http.MethodGet && r.serveImage(w, repo, ref, false) {
			return
		}
		r.mu.Lock()
		d, ok := tags[ref]
		r.mu.Unlock()
		if !ok {
			http.Error(w, `{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Docker-Content-Digest", d)
		w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
		return
	}
	r.mu.Lock()
	names := make([]string, 0, len(tags))
	for n := range tags {
		names = append(names, n)
	}
	r.mu.Unlock()
	sort.Strings(names)
	if last := req.URL.Query().Get("last"); last != "" {
		k := sort.SearchStrings(names, last)
		for k < len(names) && names[k] <= last {
			k++
		}
		names = names[k:]
	}
	n := size
	if q, err := strconv.Atoi(req.URL.Query().Get("n")); err == nil && q > 0 && (n == 0 || q < n) {
		n = q
	}
	if n > 0 && len(names) > n {
		names = names[:n]
		w.Header().Set("Link", fmt.Sprintf(`</v2/%s/tags/list?n=%d&last=%s>; rel="next"`, repo, n, names[n-1]))
	}
	json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": names})
}

// serveHubTags answers Docker Hub's tag listing, GET /v2/namespaces/{ns}/repositories/{repo}/tags
// with page, page_size and ordering (docs/vendor/registry/README.md), anonymously like the
// real one for a public repository. Each result carries name and last_updated, which is the
// order the tags were added in (AddTag again moves a tag to the front). ordering=-last_updated
// lists newest first and ordering=last_updated oldest first; any other value, and none, lists
// by name, because the real default is not documented and a client must not rely on it.
func (r *Registry) serveHubTags(w http.ResponseWriter, req *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) != 4 || parts[1] != "repositories" || parts[3] != "tags" {
		http.NotFound(w, req)
		return
	}
	repo := parts[0] + "/" + parts[2]
	r.mu.Lock()
	tags, found := r.repos[repo]
	seq := map[string]int{}
	names := make([]string, 0, len(tags))
	for n := range tags {
		names = append(names, n)
		seq[n] = r.pushed[repo+":"+n]
	}
	size := r.pageSize
	r.mu.Unlock()
	if !found {
		http.Error(w, `{"message":"httperror 404: object not found"}`, http.StatusNotFound)
		return
	}
	sort.Strings(names)
	switch req.URL.Query().Get("ordering") {
	case "-last_updated":
		sort.SliceStable(names, func(i, j int) bool { return seq[names[i]] > seq[names[j]] })
	case "last_updated":
		sort.SliceStable(names, func(i, j int) bool { return seq[names[i]] < seq[names[j]] })
	}
	n := 100
	if q, err := strconv.Atoi(req.URL.Query().Get("page_size")); err == nil && q > 0 && q < n {
		n = q
	}
	if size > 0 && size < n {
		n = size
	}
	page, _ := strconv.Atoi(req.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	lo := (page - 1) * n
	if lo > len(names) {
		lo = len(names)
	}
	hi := lo + n
	var next any
	if hi < len(names) {
		next = fmt.Sprintf("%s%s?page=%d&page_size=%d", r.URL, req.URL.Path, page+1, n)
	} else {
		hi = len(names)
	}
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	results := make([]map[string]any, 0, hi-lo)
	for _, name := range names[lo:hi] {
		results = append(results, map[string]any{"name": name, "last_updated": base.Add(time.Duration(seq[name]) * time.Minute).Format(time.RFC3339Nano)})
	}
	json.NewEncoder(w).Encode(map[string]any{"count": len(names), "next": next, "previous": nil, "results": results})
}
