package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// Docker Hub, asked on 2026-11-28 for library/nginx: ordering=last_updated lists newest first,
// ordering=-last_updated oldest first, and a value it does not know is an HTTP 500 whose body
// says "httperror 400: Invalid ordering value". This server answers the same way, so the
// request must name the ordering Hub really reads as newest first.
func hubOrderingServer(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("ordering") {
		case "last_updated":
			w.Write([]byte(`{"next":null,"results":[{"name":"1.1","last_updated":"2024-02-01T00:00:00Z"},{"name":"1.0","last_updated":"2024-01-01T00:00:00Z"}]}`))
		case "-last_updated":
			w.Write([]byte(`{"next":null,"results":[{"name":"1.0","last_updated":"2024-01-01T00:00:00Z"},{"name":"1.1","last_updated":"2024-02-01T00:00:00Z"}]}`))
		default:
			http.Error(w, `{"message":"httperror 400: Invalid ordering value","errinfo":{}}`, http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{HTTP: http.DefaultClient, Insecure: true, HubNewest: true, HubURL: srv.URL}
}

// A real page (library/nginx, ordering=last_updated, trimmed to 3 results, 2026-11-28) is read
// as newest first: the shape, with all the fields ripple ignores, is what Hub sends.
func TestTagsNewestReadsARecordedHubPage(t *testing.T) {
	body, err := os.ReadFile("testdata/hub-tags-library-nginx.json")
	if err != nil {
		t.Fatal(err)
	}
	c := hubServer(t, string(body))
	tags, ok, err := c.TagsNewest(context.Background(), "docker.io/library/nginx", []string{"stable-alpine3.24-perl"})
	if err != nil || !ok || len(tags) != 3 || tags[0] != "stable-alpine3.24-perl" {
		t.Fatalf("recorded page: %v ok=%v err=%v", tags, ok, err)
	}
}

func TestTagsNewestAsksHubForTheOrderingItReadsAsNewestFirst(t *testing.T) {
	c := hubOrderingServer(t)
	tags, ok, err := c.TagsNewest(context.Background(), "docker.io/library/nginx", []string{"1.0"})
	if err != nil || !ok || len(tags) != 2 || tags[0] != "1.1" {
		t.Fatalf("want Hub's newest-first list (1.1, 1.0), got %v ok=%v err=%v", tags, ok, err)
	}
}
