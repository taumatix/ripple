package gitea_test

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitea"
	"github.com/taumatix/ripple/internal/testutil"
)

// A signed push whose bulk is a field ripple does not read is parsed as it streams: the
// body is not held in memory nor read back from a spool file.
func TestReceiverParsesALargeSignedBodyWithoutHoldingIt(t *testing.T) {
	secret := []byte("k")
	s := &sink{}
	srv := httptest.NewServer(&gitea.Receiver{Secret: secret, Sink: s, Log: slog.New(slog.DiscardHandler)})
	defer srv.Close()
	const size = 4 << 20
	body := `{"ref":"refs/heads/main","total_commits":1,"head_commit":{"message":"` + strings.Repeat("x", size) +
		`"},"repository":{"full_name":"acme/app","default_branch":"main"},"commits":[{"added":["a.go"],"modified":["go.mod"],"removed":[]}]}`
	hdr := map[string]string{"X-Gitea-Event": "push", "X-Gitea-Signature": gitea.Sign(secret, []byte(body))}
	code, alloc := testutil.PostMeasured(t, srv.URL, hdr, body)
	if code != 202 || len(s.evs) != 1 || strings.Join(s.evs[0].Files, ",") != "a.go,go.mod" || s.evs[0].DefaultBranch != "main" {
		t.Fatalf("%d %+v", code, s.evs)
	}
	if alloc > size/3 {
		t.Fatalf("allocated %d KiB handling a %d KiB body", alloc>>10, size>>10)
	}
}

func TestReceiverRejectsAForgedBodyAfterItsParse(t *testing.T) {
	s := &sink{}
	srv := httptest.NewServer(&gitea.Receiver{Secret: []byte("k"), Sink: s, Log: slog.New(slog.DiscardHandler)})
	defer srv.Close()
	hdr := map[string]string{"X-Gitea-Event": "push", "X-Gitea-Signature": gitea.Sign([]byte("other"), []byte(pushBody))}
	if code, _ := testutil.PostMeasured(t, srv.URL, hdr, pushBody); code != 401 || len(s.evs) != 0 {
		t.Fatalf("%d %+v", code, s.evs)
	}
}

func TestReceiverRejectsAMalformedSignedPushWith400(t *testing.T) {
	secret := []byte("k")
	srv := httptest.NewServer(&gitea.Receiver{Secret: secret, Sink: &sink{}, Log: slog.New(slog.DiscardHandler)})
	defer srv.Close()
	body := `{"ref":"refs/heads/main",`
	hdr := map[string]string{"X-Gitea-Event": "push", "X-Gitea-Signature": gitea.Sign(secret, []byte(body))}
	if code, _ := testutil.PostMeasured(t, srv.URL, hdr, body); code != 400 {
		t.Fatalf("%d", code)
	}
}

// A signed push carries the repository's immutable id (the payload's `repository.id`), and
// the host answers whether an id is a repository (`GET /repositories/{id}`), for both
// flavours. The lookup goes through the host mux as the engine's does.
func TestPushCarriesRepositoryIDAndTheHostAnswersForIt(t *testing.T) {
	for _, forgejo := range []bool{false, true} {
		g := testutil.NewGitea(t, forgejo)
		g.AddRepo("acme/app", map[string]string{"go.mod": "module x\n"})
		host := gitea.NewHost(client(g), []byte("k"), slog.New(slog.DiscardHandler))
		mux, err := platform.NewMux("", host)
		if err != nil {
			t.Fatal(err)
		}
		s := &sink{}
		srv := httptest.NewServer(host.Receiver(s))
		body := strings.Replace(pushBody, `"repository":{`, `"repository":{"id":4242,`, 1)
		hdr := map[string]string{"X-Gitea-Event": "push", "X-Gitea-Signature": gitea.Sign([]byte("k"), []byte(body))}
		code, _ := testutil.PostMeasured(t, srv.URL, hdr, body)
		srv.Close()
		if code != 202 || len(s.evs) != 1 || s.evs[0].RepoID != 4242 {
			t.Fatalf("forgejo=%v: %d %+v", forgejo, code, s.evs)
		}

		r := model.Repo{FullName: "gitea:acme/app"}
		g.SetRepoID(4242, "acme/app")
		g.SetRepoID(77, "acme/other")
		for _, c := range []struct {
			id   int64
			want bool
		}{{4242, true}, {77, false}, {5, false}} { // this repo; another repo; no such id
			if got, err := platform.RepoHasID(context.Background(), mux, r, c.id); err != nil || got != c.want {
				t.Errorf("forgejo=%v id %d: %v %v, want %v", forgejo, c.id, got, err, c.want)
			}
		}
		if n := g.RepoIDCalls(); n != 3 {
			t.Errorf("%d id lookups", n)
		}
	}
}
