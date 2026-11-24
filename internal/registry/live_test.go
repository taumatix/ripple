//go:build live

package registry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/livetest"
)

// These tests pull anonymously from Docker Hub, ghcr.io and quay.io, read only (go test
// -tags live; the live workflow): tag lists, manifest digests and image config creation
// times, which go through each registry's Bearer token challenge. The fixtures are old,
// public, immutable tags whose creation year is a stable fact.

var liveImages = []struct {
	pkg, tag string
	year     int
}{
	{"docker.io/library/alpine", "3.18.0", 2023},
	{"ghcr.io/astral-sh/uv", "0.4.0", 2024},
	{"quay.io/prometheus/node-exporter", "v1.6.0", 2023},
}

func liveClient() *Client {
	hc, _ := livetest.Client()
	return &Client{HTTP: hc, UserAgent: livetest.UserAgent}
}

func TestLiveTagsDigestAndCreatedThroughTheTokenChallenge(t *testing.T) {
	c := liveClient()
	ctx := context.Background()
	for _, im := range liveImages {
		t.Run(im.pkg, func(t *testing.T) {
			tags, err := c.Tags(ctx, im.pkg)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, tg := range tags {
				if tg == im.tag {
					found = true
				}
			}
			if !found || len(tags) < 20 {
				t.Fatalf("%d tags, %s present=%v", len(tags), im.tag, found)
			}
			d1, err := c.Digest(ctx, im.pkg, im.tag)
			if err != nil {
				t.Fatal(err)
			}
			d2, err := c.Digest(ctx, im.pkg, im.tag)
			if err != nil || d1 != d2 {
				t.Fatalf("digest not stable: %q %q (%v)", d1, d2, err)
			}
			created, err := c.Created(ctx, im.pkg, im.tag)
			if err != nil {
				t.Fatal(err)
			}
			if created.IsZero() || created.Year() != im.year || created.After(time.Now()) {
				t.Errorf("created %v, want a time in %d", created, im.year)
			}
			if n := NewestOfShape(im.tag, tags); n == "" || n == "latest" {
				t.Errorf("NewestOfShape(%s) = %q, want a newer tag of the same shape", im.tag, n)
			}
		})
	}
}

func TestLiveMissingAndUnreadableImagesAreNamedErrors(t *testing.T) {
	c := liveClient()
	for _, pkg := range []string{"docker.io/library/ripple-no-such-image-zz9", "ghcr.io/ripple-no-such-owner/zz9", "quay.io/ripple-no-such-owner/zz9"} {
		_, err := c.Tags(context.Background(), pkg)
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrDenied) {
			t.Errorf("%s: %v, want ErrNotFound or ErrDenied (a private image answers like a missing one)", pkg, err)
		}
	}
	if _, err := c.Digest(context.Background(), "docker.io/library/alpine", "ripple-no-such-tag-zz9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing tag: %v, want ErrNotFound", err)
	}
}
