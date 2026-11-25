package github_test

import (
	"context"
	"testing"

	"github.com/taumatix/ripple/internal/platform"
)

// The open-PR listing reports the description, which the engine compares with the one
// ripple sent to notice a person's edit.
func TestListOpenPRsReportsBody(t *testing.T) {
	c, repo, _, _ := throttledClient(t)
	if _, err := c.CreatePR(context.Background(), repo, platform.PRSpec{Head: "main", Base: "main", Title: "bump", Body: "the body"}); err != nil {
		t.Fatal(err)
	}
	open, err := c.ListOpenPRs(context.Background(), repo, "")
	if err != nil || len(open) != 1 || open[0].Body != "the body" {
		t.Fatalf("%+v %v", open, err)
	}
}
