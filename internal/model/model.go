// Package model holds the plain data types shared by every ripple component.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

const DatasourceGo = "go"

// DatasourceGitHubTags is the tags of a GitHub repository: the versions of a
// GitHub Action or reusable workflow, named by their owner/repo.
const DatasourceGitHubTags = "github-tags"

// DatasourceDocker is the tags of a container image on a registry: the versions of
// a Dockerfile's base image, named "registry/repository" (docker.io/library/nginx).
const DatasourceDocker = "docker"

// DatasourceNpm is the versions of a package on the npm registry: the ranges of a
// package.json's dependencies, named by the package name (@scope/name for a scoped one).
const DatasourceNpm = "npm"

// DatasourceCargo is the versions of a crate on crates.io (its sparse index): the
// requirements of a Cargo.toml's dependencies, named by the crate name.
const DatasourceCargo = "cargo"

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
	// RepoID is the host's immutable repository id when a signed delivery vouched for it,
	// else 0 (a repo registered by an install event alone). It survives renames, which is
	// what lets a push under a new name be tied to the record held under the old one.
	RepoID int64
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
	// HeadSHA and BodyHash describe the pull request ripple opened for the job: the
	// commit its head branch pointed at (empty when the host's create response does not
	// say) and BodyHash of the body it sent. A later pass compares them with what the
	// host reports to notice an edit the host's own event feed cannot show. Both are
	// empty on a job that finished before they were recorded, and for jobs that opened
	// no PR.
	HeadSHA  string
	BodyHash string
}

// BodyHash is the hash recorded in Job.BodyHash: SHA-256 of body, hex encoded, after
// line endings are normalised to \n and trailing whitespace is dropped, because hosts
// differ in whether they keep the CRLFs and the final newline of a body they were sent.
func BodyHash(body string) string {
	body = strings.TrimRight(strings.ReplaceAll(body, "\r\n", "\n"), " \t\r\n")
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
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
