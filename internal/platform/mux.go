package platform

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/taumatix/ripple/internal/model"
)

var hostName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Mux is the Platform the engine holds when several hosts are registered: it routes
// each repo to its host by the "<host>:" prefix of its stored name, strips the
// prefix, and passes the optional capabilities through per host. Repos of the
// default host carry no prefix, so names stored before a second host existed
// (GitHub's owner/repo) keep their meaning.
type Mux struct {
	def   string
	hosts map[string]*Host
}

// NewMux registers hosts; def names the host whose repos are unprefixed ("" for none).
func NewMux(def string, hosts ...*Host) (*Mux, error) {
	m := &Mux{def: def, hosts: map[string]*Host{}}
	for _, h := range hosts {
		if !hostName.MatchString(h.Name) {
			return nil, fmt.Errorf("invalid host name %q", h.Name)
		}
		if h.Platform == nil {
			return nil, fmt.Errorf("host %s has no platform", h.Name)
		}
		if _, dup := m.hosts[h.Name]; dup {
			return nil, fmt.Errorf("host %s registered twice", h.Name)
		}
		m.hosts[h.Name] = h
	}
	if _, ok := m.hosts[def]; def != "" && !ok {
		return nil, fmt.Errorf("default host %s is not registered", def)
	}
	return m, nil
}

// RepoName is the stored name of the host's repo path.
func (m *Mux) RepoName(host, path string) string {
	if host == m.def {
		return path
	}
	return host + ":" + path
}

// HostOf returns the host name and host-native path of a stored repo name.
func (m *Mux) HostOf(name string) (host, path string) {
	if h, p, ok := strings.Cut(name, ":"); ok {
		return h, p
	}
	return m.def, name
}

func (m *Mux) route(repo model.Repo) (Platform, model.Repo, error) {
	h, p := m.HostOf(repo.FullName)
	host, ok := m.hosts[h]
	if !ok || h == "" {
		return nil, repo, fmt.Errorf("repository %q belongs to no registered host", repo.FullName)
	}
	repo.FullName = p
	return host.Platform, repo, nil
}

func (m *Mux) Checkout(ctx context.Context, repo model.Repo) (Workspace, error) {
	p, r, err := m.route(repo)
	if err != nil {
		return nil, err
	}
	return p.Checkout(ctx, r)
}

func (m *Mux) ListOpenPRs(ctx context.Context, repo model.Repo, prefix string) ([]PR, error) {
	p, r, err := m.route(repo)
	if err != nil {
		return nil, err
	}
	return p.ListOpenPRs(ctx, r, prefix)
}

func (m *Mux) CreatePR(ctx context.Context, repo model.Repo, spec PRSpec) (PR, error) {
	p, r, err := m.route(repo)
	if err != nil {
		return PR{}, err
	}
	return p.CreatePR(ctx, r, spec)
}

// RequestAutoMerge returns ErrNoAutoMerge for a host without the capability.
func (m *Mux) RequestAutoMerge(ctx context.Context, repo model.Repo, pr PR) error {
	p, r, err := m.route(repo)
	if err != nil {
		return err
	}
	return RequestAutoMerge(ctx, p, r, pr)
}

// CloseUntouchedPR returns ErrNoClosePR for a host without the capability.
func (m *Mux) CloseUntouchedPR(ctx context.Context, repo model.Repo, pr PR, comment string) (bool, error) {
	p, r, err := m.route(repo)
	if err != nil {
		return false, err
	}
	return CloseUntouchedPR(ctx, p, r, pr, comment)
}

func (m *Mux) RateLimitedUntil(repo model.Repo) time.Time {
	p, r, err := m.route(repo)
	if err != nil {
		return time.Time{}
	}
	return RateLimitedUntil(p, r)
}

func (m *Mux) ReadsLegacyConfig(repo model.Repo) bool {
	p, r, err := m.route(repo)
	return err == nil && ReadsLegacyConfig(p, r)
}

// RepoHasID routes to the repo's host; a host without the capability answers
// ErrNoRepoIdentity.
func (m *Mux) RepoHasID(ctx context.Context, repo model.Repo, id int64) (bool, error) {
	p, r, err := m.route(repo)
	if err != nil {
		return false, err
	}
	return RepoHasID(ctx, p, r, id)
}

// FindIssue, CreateIssue and UpdateIssueBody route to the repo's host and answer
// ErrNoIssues for a host without the capability.
func (m *Mux) FindIssue(ctx context.Context, repo model.Repo, label, marker string) (Issue, bool, error) {
	p, r, err := m.route(repo)
	if err != nil {
		return Issue{}, false, err
	}
	return FindIssue(ctx, p, r, label, marker)
}

func (m *Mux) CreateIssue(ctx context.Context, repo model.Repo, spec IssueSpec) (Issue, error) {
	p, r, err := m.route(repo)
	if err != nil {
		return Issue{}, err
	}
	return CreateIssue(ctx, p, r, spec)
}

func (m *Mux) CloseIssue(ctx context.Context, repo model.Repo, number int) error {
	p, r, err := m.route(repo)
	if err != nil {
		return err
	}
	return CloseIssue(ctx, p, r, number)
}

func (m *Mux) ReopenIssue(ctx context.Context, repo model.Repo, number int) error {
	p, r, err := m.route(repo)
	if err != nil {
		return err
	}
	return ReopenIssue(ctx, p, r, number)
}

func (m *Mux) UpdateIssueBody(ctx context.Context, repo model.Repo, number int, body string) error {
	p, r, err := m.route(repo)
	if err != nil {
		return err
	}
	return UpdateIssueBody(ctx, p, r, number, body)
}
