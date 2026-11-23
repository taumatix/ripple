package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func azureDeps(env map[string]string) HostDeps {
	return HostDeps{Getenv: func(k string) string { return env[k] }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestAzureDevOpsHostIsConfiguredOnlyByItsOwnSettings(t *testing.T) {
	ctx := context.Background()
	if h, err := buildAzureDevOps(ctx, azureDeps(nil)); h != nil || err != nil {
		t.Fatalf("unconfigured: %v, %v", h, err)
	}
	if _, err := buildAzureDevOps(ctx, azureDeps(map[string]string{"RIPPLE_AZUREDEVOPS_ORG": "acme"})); err == nil {
		t.Error("an organization without a token must be an error, not a silently absent host")
	}
	h, err := buildAzureDevOps(ctx, azureDeps(map[string]string{"RIPPLE_AZUREDEVOPS_ORG": "acme", "RIPPLE_AZUREDEVOPS_PAT": "p", "RIPPLE_AZUREDEVOPS_WEBHOOK_PASSWORD": "w"}))
	if err != nil || h == nil || h.Name != "azuredevops" || h.Receiver == nil {
		t.Fatalf("configured: %v, %v", h, err)
	}
}
