// Package model holds the plain data types shared by every ripple component.
package model

import (
	"strings"
	"time"
)

const DatasourceGo = "go"

// DatasourceGitHubTags is the tags of a GitHub repository: the versions of a
// GitHub Action or reusable workflow, named by their owner/repo.
const DatasourceGitHubTags = "github-tags"

// DatasourceGoToolchain is the Go release train itself: the `go` directive
// (package PackageGoDirective) and the `toolchain` line (PackageGoToolchain) of a
// go.mod. They are dependencies of the module on the language, not on a module.
const (
	DatasourceGoToolchain = "go-toolchain"
	PackageGoDirective    = "go"
	PackageGoToolchain    = "toolchain"
)

type Repo struct {
	FullName       string
	InstallationID int64
	DefaultBranch  string
	// Config is the raw per-repo config file (.ripple.yaml or its deprecated alias), empty when absent.
	Config []byte
}

// Dependency is one version-pinned reference found in a repo file.
type Dependency struct {
	Repo       string
	File       string
	Manager    string
	Datasource string
	Package    string
	Version    string
}

type Release struct {
	Datasource string
	Package    string
	Version    string
	Time       time.Time
}

type JobState string

const (
	JobPending JobState = "pending"
	JobDone    JobState = "done"
	JobSkipped JobState = "skipped"
	JobFailed  JobState = "failed"
)

// Job is the intent to move one dependency of one repo to one version.
type Job struct {
	Key        string
	Repo       string
	Datasource string
	Package    string
	Version    string
	ReleasedAt time.Time
	State      JobState
	NotBefore  time.Time
	Attempts   int
	PRURL      string
	Reason     string
}

func JobKey(repo, datasource, pkg, version string) string {
	return strings.Join([]string{repo, datasource, pkg, version}, "|")
}

// Event is one received webhook action on one repo, persisted until it is handled so
// a restart or a busy worker pool does not lose it. A delivery naming several repos is
// split into one Event per repo, which is what lets workers keep each repo's events in
// order while running other repos of the same installation concurrently.
type Event struct {
	// ID is unique; the delivery id plus the repo's index in the delivery, so a
	// redelivery of a delivery still queued is not queued twice.
	ID string
	// Seq orders events: lower is older. It is assigned at receipt.
	Seq            int64
	InstallationID int64
	Kind           string // "repos_added", "repos_removed", "push", "repo_renamed", "default_branch_changed"
	Repo           string
	// Ref is a push's ref. The two repo-state kinds also carry it, for events queued
	// before the typed fields below existed and for a rolling upgrade: repo_renamed the
	// new stored repo name, default_branch_changed the new default branch as
	// refs/heads/<name>. ripple:deprecated event-ref-overload
	Ref   string
	Files []string
	// NewRepo is the new stored repo name of a repo_renamed event (Repo is the old one).
	NewRepo string
	// DefaultBranch is the default branch name (no refs/heads/ prefix) the host reported:
	// the new one for default_branch_changed, and the one a push payload claimed.
	DefaultBranch string
	// RepoID is the host's immutable repository id when the host provides one, else 0.
	RepoID int64
	// Attempts counts failed handling attempts; NotBefore delays the next one.
	Attempts  int
	NotBefore time.Time
	// LastError and LastAttemptAt describe the latest failed attempt.
	LastError     string
	LastAttemptAt time.Time
	// DeadAt is when the event used up its attempts and was dead-lettered; the zero
	// value is a live event. A dead event is kept for an operator to list and requeue
	// (see store.Store), and is never handled until requeued.
	DeadAt time.Time
}

// Dead reports whether the event was dead-lettered.
func (e Event) Dead() bool { return !e.DeadAt.IsZero() }
