package gitea

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/webhook"
)

const (
	maxBody = 8 << 20
)

// Sign is the X-Gitea-Signature / X-Forgejo-Signature value for body: the
// lowercase hex HMAC-SHA256, no prefix (docs/vendor/gitea/webhooks.md).
func Sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// signature finds the delivery's signature as lowercase hex, whichever header
// carries it. Gitea sends X-Gitea-Signature and X-Hub-Signature-256 (sha256=...);
// Forgejo's documented example reads X-Forgejo-Signature. Whether Forgejo also
// sends the other two is unverified, so all three are accepted: each is an HMAC of
// the same body under the same secret, so any one that verifies authenticates it.
func signature(h http.Header) (string, bool) {
	for _, name := range []string{"X-Forgejo-Signature", "X-Gitea-Signature", "X-Hub-Signature-256"} {
		v := h.Get(name)
		if name == "X-Hub-Signature-256" {
			v = strings.TrimPrefix(v, "sha256=")
		}
		if len(v) == sha256.Size*2 {
			if _, err := hex.DecodeString(v); err == nil {
				return strings.ToLower(v), true
			}
		}
	}
	return "", false
}

func header(h http.Header, names ...string) string {
	for _, n := range names {
		if v := h.Get(n); v != "" {
			return v
		}
	}
	return ""
}

// Receiver is the webhook http.Handler for Gitea and Forgejo. It authenticates a
// delivery from its headers before reading the body, bounds the body and the number
// held at once, and drops a repeated delivery id.
type Receiver struct {
	Secret []byte
	Sink   platform.Sink
	Log    *slog.Logger
	Now    func() time.Time

	guard  webhook.Guard
	dedupe webhook.Dedupe
}

func (h *Receiver) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	delivery := header(r.Header, "X-Forgejo-Delivery", "X-Gitea-Delivery", "X-GitHub-Delivery")
	sig, sigOK := signature(r.Header)
	if len(h.Secret) == 0 || !sigOK {
		h.Log.Warn("webhook signature rejected", "delivery", delivery)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	want, _ := hex.DecodeString(sig)
	// The body is parsed as it streams and never held; the parse of a forgery is
	// discarded with the 401. Events ripple does not act on are only hashed.
	name := header(r.Header, "X-Forgejo-Event", "X-Gitea-Event", "X-GitHub-Event")
	var (
		ev  platform.Event
		ok  bool
		err error
	)
	var consume func(io.Reader) error
	if name == "push" || name == "repository" {
		consume = func(rd io.Reader) error { ev, ok, err = parse(name, rd); return nil }
	}
	_, res := h.guard.ReadVerifiedStream(w, r, maxBody, h.Secret, nil, func(got []byte) bool { return hmac.Equal(got, want) }, consume)
	switch res {
	case webhook.ReadAnswered:
		return
	case webhook.ReadForged:
		h.Log.Warn("webhook signature rejected", "delivery", delivery)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	ev.Delivery = delivery
	ev.BodyKey = webhook.SignatureKey(sig)
	keys := []string{delivery, ev.BodyKey}
	if !h.dedupe.Claim(h.now(), keys...) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if err := h.Sink.HandleEvent(r.Context(), ev); err != nil {
		h.dedupe.Release(keys...)
		h.Log.Error("webhook sink", "event", ev.Kind, "err", err)
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// parse turns a delivery into an Event as it streams, keeping only the fields ripple acts
// on; ok=false for events ripple ignores.
func parse(name string, r io.Reader) (platform.Event, bool, error) {
	x := &webhook.Extractor{}
	switch name {
	case "push":
		var p struct {
			Ref, FullName, DefaultBranch string
			ID                           int64
			TotalCommits                 *int64
		}
		type commit struct{ added, modified, removed []string }
		var commits []commit
		c := webhook.Obj(map[string]*webhook.Node{
			"added":    x.StrsInto(func() *[]string { return &commits[len(commits)-1].added }),
			"modified": x.StrsInto(func() *[]string { return &commits[len(commits)-1].modified }),
			"removed":  x.StrsInto(func() *[]string { return &commits[len(commits)-1].removed }),
		}).OnObject(func() { commits = append(commits, commit{}) })
		err := webhook.Extract(r, webhook.Obj(map[string]*webhook.Node{
			"ref":           x.Str(&p.Ref),
			"total_commits": x.I64Ptr(&p.TotalCommits),
			"repository": webhook.Obj(map[string]*webhook.Node{
				"id":             x.I64(&p.ID),
				"full_name":      x.Str(&p.FullName),
				"default_branch": x.Str(&p.DefaultBranch),
			}),
			"commits": webhook.Array(c),
		}))
		if err != nil {
			return platform.Event{}, false, err
		}
		var files []string
		// A payload that lists fewer commits than were pushed is truncated, and
		// then its file lists are incomplete: leave Files empty to force a re-scan.
		if p.TotalCommits == nil || *p.TotalCommits <= int64(len(commits)) {
			seen := map[string]bool{}
			for _, c := range commits {
				for _, l := range [][]string{c.added, c.modified, c.removed} {
					for _, f := range l {
						if !seen[f] {
							seen[f] = true
							files = append(files, f)
						}
					}
				}
			}
		}
		return platform.Event{Kind: platform.Push, Repos: []string{p.FullName}, Ref: p.Ref,
			DefaultBranch: p.DefaultBranch, Files: files, RepoID: p.ID}, true, nil
	case "repository":
		var p struct{ Action, FullName string }
		err := webhook.Extract(r, webhook.Obj(map[string]*webhook.Node{
			"action":     x.Str(&p.Action),
			"repository": webhook.Obj(map[string]*webhook.Node{"full_name": x.Str(&p.FullName)}),
		}))
		if err != nil {
			return platform.Event{}, false, err
		}
		switch p.Action {
		case "created":
			return platform.Event{Kind: platform.ReposAdded, Repos: []string{p.FullName}}, true, nil
		case "deleted":
			return platform.Event{Kind: platform.ReposRemoved, Repos: []string{p.FullName}}, true, nil
		}
	}
	return platform.Event{}, false, nil
}
