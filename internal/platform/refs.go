package platform

import (
	"context"
	"errors"
	"time"

	"github.com/taumatix/ripple/internal/model"
)

// ErrNoTags means the host cannot list the tags of a repository, so the
// github-actions manager cannot find versions through it.
var ErrNoTags = errors.New("host cannot read tags")

// TagRelease is the release a host published for a tag.
type TagRelease struct {
	// URL is the release page, where its changelog is.
	URL       string
	Published time.Time
}

// TagSource is the optional capability to read the tags of any repository the
// credential of via can see (via is the dependent repo whose installation is used;
// target is the "owner/repo" read). It backs the github-tags datasource, the
// versions of a GitHub Action.
type TagSource interface {
	// ListTags returns the names of target's tags, in no particular order.
	ListTags(ctx context.Context, via model.Repo, target string) ([]string, error)
	// TagCommit returns the commit tag points at, dereferencing an annotated tag.
	TagCommit(ctx context.Context, via model.Repo, target, tag string) (string, error)
	// ReleaseOf returns the release published for tag; false when there is none.
	ReleaseOf(ctx context.Context, via model.Repo, target, tag string) (TagRelease, bool, error)
}

// ListTags is p's ListTags, or ErrNoTags.
func ListTags(ctx context.Context, p Platform, via model.Repo, target string) ([]string, error) {
	t, ok := p.(TagSource)
	if !ok {
		return nil, ErrNoTags
	}
	return t.ListTags(ctx, via, target)
}

// TagCommit is p's TagCommit, or ErrNoTags.
func TagCommit(ctx context.Context, p Platform, via model.Repo, target, tag string) (string, error) {
	t, ok := p.(TagSource)
	if !ok {
		return "", ErrNoTags
	}
	return t.TagCommit(ctx, via, target, tag)
}

// ReleaseOf is p's ReleaseOf, or ErrNoTags.
func ReleaseOf(ctx context.Context, p Platform, via model.Repo, target, tag string) (TagRelease, bool, error) {
	t, ok := p.(TagSource)
	if !ok {
		return TagRelease{}, false, ErrNoTags
	}
	return t.ReleaseOf(ctx, via, target, tag)
}

// ListTags, TagCommit and ReleaseOf route to the dependent repo's host.
func (m *Mux) ListTags(ctx context.Context, via model.Repo, target string) ([]string, error) {
	p, r, err := m.route(via)
	if err != nil {
		return nil, err
	}
	return ListTags(ctx, p, r, target)
}

func (m *Mux) TagCommit(ctx context.Context, via model.Repo, target, tag string) (string, error) {
	p, r, err := m.route(via)
	if err != nil {
		return "", err
	}
	return TagCommit(ctx, p, r, target, tag)
}

func (m *Mux) ReleaseOf(ctx context.Context, via model.Repo, target, tag string) (TagRelease, bool, error) {
	p, r, err := m.route(via)
	if err != nil {
		return TagRelease{}, false, err
	}
	return ReleaseOf(ctx, p, r, target, tag)
}
