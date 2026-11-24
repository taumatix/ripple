package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func hubServer(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	t.Cleanup(srv.Close)
	return &Client{HTTP: http.DefaultClient, Insecure: true, HubNewest: true, HubURL: srv.URL}
}

func TestTagsNewestRefusesAListingNotNewestFirst(t *testing.T) {
	// the second result is newer than the first: Hub ignored the ordering.
	c := hubServer(t, `{"next":null,"results":[{"name":"1.0","last_updated":"2024-01-01T00:00:00Z"},{"name":"1.1","last_updated":"2024-02-01T00:00:00Z"}]}`)
	if tags, ok, err := c.TagsNewest(context.Background(), "docker.io/library/nginx", []string{"1.0"}); ok || err != nil {
		t.Fatalf("must fall back: %v %v %v", tags, ok, err)
	}
}

func TestTagsNewestAnswersOnlyWhenOptedInAndOnHub(t *testing.T) {
	body := `{"next":null,"results":[{"name":"1.1","last_updated":"2024-02-01T00:00:00Z"},{"name":"1.0","last_updated":"2024-01-01T00:00:00Z"}]}`
	c := hubServer(t, body)
	ctx := context.Background()
	if tags, ok, err := c.TagsNewest(ctx, "docker.io/library/nginx", []string{"1.0"}); !ok || err != nil || len(tags) != 2 {
		t.Fatalf("opted in: %v %v %v", tags, ok, err)
	}
	for name, pkg := range map[string]string{"ghcr": "ghcr.io/org/img", "not two segments": "docker.io/nginx", "no current": "docker.io/library/nginx"} {
		cur := []string{"1.0"}
		if name == "no current" {
			cur = nil
		}
		if _, ok, _ := c.TagsNewest(ctx, pkg, cur); ok {
			t.Errorf("%s: must not use Hub's listing", name)
		}
	}
	c.HubNewest = false
	if _, ok, _ := c.TagsNewest(ctx, "docker.io/library/nginx", []string{"1.0"}); ok {
		t.Error("off by default")
	}
}
