package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestGerritHostNeedsItsWholeConfiguration(t *testing.T) {
	ctx := context.Background()
	deps := func(m map[string]string) HostDeps {
		return HostDeps{Getenv: func(k string) string { return m[k] }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	}
	if h, err := buildGerrit(ctx, deps(nil)); h != nil || err != nil {
		t.Fatalf("unconfigured: %v, %v", h, err)
	}
	base := map[string]string{"RIPPLE_GERRIT_URL": "https://g.example", "RIPPLE_GERRIT_USER": "bot", "RIPPLE_GERRIT_TOKEN": "t"}
	if _, err := buildGerrit(ctx, deps(base)); err == nil {
		t.Error("a missing webhook secret must be an error")
	}
	if _, err := buildGerrit(ctx, deps(map[string]string{"RIPPLE_GERRIT_URL": "https://g.example"})); err == nil {
		t.Error("a URL without credentials must be an error")
	}
	base["RIPPLE_GERRIT_WEBHOOK_SECRET"] = "s"
	h, err := buildGerrit(ctx, deps(base))
	if err != nil || h == nil || h.Name != "gerrit" || h.Receiver == nil {
		t.Fatalf("configured: %v, %v", h, err)
	}
}
