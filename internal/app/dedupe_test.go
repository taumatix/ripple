package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitlab"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/webhook"
)

const gitlabPush = `{"object_kind":"push","ref":"refs/heads/main","total_commits_count":1,"project":{"path_with_namespace":"g/app","default_branch":"main"},"commits":[{"modified":["go.mod"]}]}`

// gitlabReplica is a fresh process serving GitLab webhooks (secret token "s") over st.
func gitlabReplica(st store.Store) *App {
	a := replica(st, 0)
	a.Hosts = []*platform.Host{gitlab.NewHost(&gitlab.Client{}, gitlab.Receiver{SecretToken: "s"}, nil)}
	return a
}

func postGitLab(t *testing.T, a *App, uuid, id string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/webhook/gitlab", strings.NewReader(gitlabPush))
	r.Header.Set("X-Gitlab-Event", "Push Hook")
	r.Header.Set("X-Gitlab-Token", "s")
	r.Header.Set("X-Gitlab-Event-UUID", uuid)
	r.Header.Set("webhook-id", id)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}
}

// Recursive webhooks share X-Gitlab-Event-UUID, so two distinct deliveries under one UUID
// are both handled; webhook-id (unique per message, kept across retries) is the key, and
// the store's table drops a retry that reaches a replica which never saw it.
func TestGitLabDeliveriesAreKeyedOnWebhookIDAcrossReplicas(t *testing.T) {
	st := store.NewMemory()
	postGitLab(t, gitlabReplica(st), "shared-uuid", "msg-1")
	postGitLab(t, gitlabReplica(st), "shared-uuid", "msg-2")
	if got := queued(t, st); len(got) != 2 {
		t.Fatalf("two distinct deliveries sharing an event UUID: queued %v", got)
	}
	drain(t, st)
	postGitLab(t, gitlabReplica(st), "shared-uuid", "msg-2") // a retry, on another replica
	if got := queued(t, st); len(got) != 0 {
		t.Fatalf("a retried delivery was handled again: %v", got)
	}
}

// With signature dedupe on, a body replayed under a new delivery id is dropped by a
// replica that never saw the first: the signature is claimed in the store's table too.
func TestSignatureDedupeSpansReplicas(t *testing.T) {
	defer func(old bool) { webhook.DefaultDedupeBySignature = old }(webhook.DefaultDedupeBySignature)
	webhook.DefaultDedupeBySignature = true
	st := store.NewMemory()
	postSigned(replica(st, 0).Handler(), "d1")
	drain(t, st)
	if c := postSigned(replica(st, 0).Handler(), "d2"); c != 202 {
		t.Fatalf("replay = %d", c)
	}
	if got := queued(t, st); len(got) != 0 {
		t.Fatalf("a replayed body under a new id was handled again: %v", got)
	}
}

// With it off (the default) the same replay is handled, as before.
func TestSignatureReplayIsHandledWhileTheOptionIsOff(t *testing.T) {
	st := store.NewMemory()
	postSigned(replica(st, 0).Handler(), "d1")
	drain(t, st)
	postSigned(replica(st, 0).Handler(), "d2")
	if got := queued(t, st); len(got) != 1 {
		t.Fatalf("queued %v", got)
	}
}
