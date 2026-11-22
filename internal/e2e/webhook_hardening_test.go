package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/taumatix/ripple/internal/store"
)

// post sends body to the real server with explicit headers and returns the status,
// or -1 when the server cut the connection (acceptable for rejected oversized bodies).
func (e *env) post(event, delivery, sig string, body []byte) int {
	req, _ := http.NewRequest(http.MethodPost, e.hook, bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", event)
	if delivery != "" {
		req.Header.Set("X-GitHub-Delivery", delivery)
	}
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func installBody(id int, repos ...string) []byte {
	b, _ := json.Marshal(map[string]any{"action": "created", "installation": map[string]any{"id": id}, "repositories": repoList(repos...)})
	return b
}

// A captured signed delivery, replayed, is handled once: the second copy is
// acknowledged but never reaches the engine, so the repo is scanned exactly once.
func TestReplayedDeliveryScansOnce(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		baseRepos(e)
		body := installBody(7, "acme/uses-lib")
		for i := 0; i < 3; i++ {
			if c := e.post("installation", "delivery-1", sign(body), body); c != http.StatusAccepted {
				t.Fatalf("delivery %d: %d", i, c)
			}
		}
		e.waitRepo("acme/uses-lib")
		// A later distinct delivery is queued behind the replays, so once it has
		// been processed every replay has too.
		sentinel := installBody(7, "acme/unrelated")
		if c := e.post("installation", "delivery-2", sign(sentinel), sentinel); c != http.StatusAccepted {
			t.Fatal(c)
		}
		e.waitRepo("acme/unrelated")
		first := e.plat.Calls("acme/uses-lib")
		if first == 0 {
			t.Fatal("repo was never scanned")
		}
		// Replays after the fact stay dropped; a fresh delivery behind them proves they were processed.
		if c := e.post("installation", "delivery-1", sign(body), body); c != http.StatusAccepted {
			t.Fatal(c)
		}
		last := installBody(7, "acme/also-uses")
		e.post("installation", "delivery-3", sign(last), last)
		e.waitRepo("acme/also-uses")
		if n := e.plat.Calls("acme/uses-lib"); n != first {
			t.Fatalf("replay rescanned the repo: calls %d -> %d", first, n)
		}
	})
}

func TestUnauthenticatedFloodIsBounded(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		forged := "sha256=" + strings.Repeat("ab", 32)
		// Not even well-formed: refused without reading the body.
		if c := e.post("push", "x", "", bytes.Repeat([]byte("x"), 30<<20)); c != http.StatusUnauthorized && c != -1 {
			t.Fatalf("unsigned 30MB: %d", c)
		}
		// Declared larger than GitHub's cap, even if "signed".
		if c := e.post("push", "x", forged, bytes.Repeat([]byte("x"), 26<<20)); c != http.StatusRequestEntityTooLarge && c != -1 {
			t.Fatalf("oversized: %d", c)
		}
		// A flood of forged-signature posts never gets a 2xx, and the receiver still
		// serves a genuine delivery afterwards.
		var wg sync.WaitGroup
		codes := make(chan int, 64)
		for i := 0; i < 64; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); codes <- e.post("push", "", forged, bytes.Repeat([]byte("y"), 4<<20)) }()
		}
		wg.Wait()
		close(codes)
		for c := range codes {
			if c == http.StatusAccepted {
				t.Fatalf("forged request accepted")
			}
		}
		body := installBody(7, "acme/late")
		if c := e.post("installation", "genuine", sign(body), body); c != http.StatusAccepted {
			t.Fatalf("genuine delivery after flood: %d", c)
		}
	})
}
