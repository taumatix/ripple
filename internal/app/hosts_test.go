package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/store"
)

// listingHost is a host with no webhooks: it is discovered by listing.
type listingHost struct {
	mu    sync.Mutex
	repos []string
}

func (l *listingHost) set(names ...string) { l.mu.Lock(); l.repos = names; l.mu.Unlock() }

func (l *listingHost) ListRepos(context.Context) ([]model.Repo, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []model.Repo
	for _, n := range l.repos {
		out = append(out, model.Repo{FullName: n})
	}
	return out, nil
}

func (l *listingHost) Checkout(context.Context, model.Repo) (platform.Workspace, error) {
	return nil, fmt.Errorf("not used")
}
func (l *listingHost) ListOpenPRs(context.Context, model.Repo, string) ([]platform.PR, error) {
	return nil, nil
}
func (l *listingHost) CreatePR(context.Context, model.Repo, platform.PRSpec) (platform.PR, error) {
	return platform.PR{}, nil
}

func queued(t *testing.T, st store.Store) []string {
	t.Helper()
	evs, err := st.ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		out = append(out, e.Kind+" "+e.Repo)
	}
	sort.Strings(out)
	return out
}

func TestDiscoverQueuesTheDifferenceBetweenListings(t *testing.T) {
	st := store.NewMemory()
	lh := &listingHost{}
	a := &App{Hosts: []*platform.Host{{Name: "gerrit", Platform: lh}}, Queue: &Queue{Store: st, Process: newGate().process}}
	ctx := context.Background()

	lh.set("a", "b")
	a.Discover(ctx)
	if got := fmt.Sprint(queued(t, st)); got != "[repos_added gerrit:a repos_added gerrit:b]" {
		t.Fatalf("first listing queued %s", got)
	}
	drain(t, st)

	a.Discover(ctx) // unchanged: nothing to do, and no event is queued
	if got := queued(t, st); len(got) != 0 {
		t.Fatalf("unchanged listing queued %v", got)
	}

	lh.set("b", "c")
	a.Discover(ctx)
	if got := fmt.Sprint(queued(t, st)); got != "[repos_added gerrit:c repos_removed gerrit:a]" {
		t.Fatalf("changed listing queued %s", got)
	}
}

func drain(t *testing.T, st store.Store) {
	t.Helper()
	evs, _ := st.ListEvents(context.Background())
	for _, e := range evs {
		if err := st.DeleteEvent(context.Background(), e.ID); err != nil {
			t.Fatal(err)
		}
	}
}

// recordingReceiver delivers one fixed event to its sink on any request.
func recordingReceiver(ev platform.Event) func(platform.Sink) http.Handler {
	return func(s platform.Sink) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := s.HandleEvent(r.Context(), ev); err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		})
	}
}

func TestEachHostReceivesOnItsOwnPathAndItsReposArePrefixed(t *testing.T) {
	st := store.NewMemory()
	ev := platform.Event{Kind: platform.ReposAdded, Delivery: "d1", Repos: []string{"g/p"}}
	a := &App{DefaultHost: "github", Hosts: []*platform.Host{
		{Name: "github", Platform: &listingHost{}, Receiver: recordingReceiver(ev)},
		{Name: "gitlab", Platform: &listingHost{}, Receiver: recordingReceiver(ev)},
	}, Queue: &Queue{Store: st, Process: newGate().process}}
	h := a.Handler()
	for _, path := range []string{"/webhook", "/webhook/github", "/webhook/gitlab", "/webhook/nobody"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		want := http.StatusAccepted
		if path == "/webhook/nobody" {
			want = http.StatusNotFound
		}
		if rec.Code != want {
			t.Errorf("POST %s = %d, want %d", path, rec.Code, want)
		}
	}
	evs, _ := st.ListEvents(context.Background())
	var ids []string
	for _, e := range evs {
		ids = append(ids, e.ID+"="+e.Repo)
	}
	sort.Strings(ids)
	// /webhook and /webhook/github are the same delivery of the default host, queued once.
	if got := fmt.Sprint(ids); got != "[d1/0=g/p gitlab/d1/0=gitlab:g/p]" {
		t.Fatalf("queued %s", got)
	}
}

func TestDefaultHostNamesCannotPoseAsAnotherHost(t *testing.T) {
	st := store.NewMemory()
	a := &App{Hosts: []*platform.Host{{Name: "github", Platform: &listingHost{}}}, Queue: &Queue{Store: st, Process: newGate().process}}
	if err := a.HandleEvent(context.Background(), platform.Event{Kind: platform.ReposAdded, Delivery: "d", Repos: []string{"gitlab:g/p", "acme/ok"}}); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(queued(t, st)); got != "[repos_added acme/ok]" {
		t.Fatalf("queued %s", got)
	}
}

// The deprecated Event.Ref overload stays populated beside the typed fields for the
// deprecation window (MIGRATION.md, event-ref-overload).
func TestEnqueueKeepsTheDeprecatedRefBesideTypedFields(t *testing.T) {
	st := store.NewMemory()
	a := &App{Queue: &Queue{Store: st, Process: newGate().process}}
	ctx := context.Background()
	if err := a.enqueue(ctx, "github", platform.Event{Delivery: "d1", Kind: platform.RepoRenamed, Repos: []string{"o/old"}, NewRepo: "o/new"}); err != nil {
		t.Fatal(err)
	}
	if err := a.enqueue(ctx, "github", platform.Event{Delivery: "d2", Kind: platform.DefaultBranchChanged, Repos: []string{"o/new"}, DefaultBranch: "trunk"}); err != nil {
		t.Fatal(err)
	}
	evs, err := st.ListEvents(ctx)
	if err != nil || len(evs) != 2 {
		t.Fatalf("events %v, err %v", evs, err)
	}
	for _, e := range evs {
		switch e.Kind {
		case string(platform.RepoRenamed):
			if e.NewRepo != "o/new" || e.Ref != "o/new" {
				t.Errorf("rename: NewRepo %q Ref %q", e.NewRepo, e.Ref)
			}
		case string(platform.DefaultBranchChanged):
			if e.DefaultBranch != "trunk" || e.Ref != "refs/heads/trunk" {
				t.Errorf("default branch: DefaultBranch %q Ref %q", e.DefaultBranch, e.Ref)
			}
		}
	}
}
