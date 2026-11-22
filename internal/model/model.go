// Package model holds the plain data types shared by every ripple component.
package model

import (
	"strings"
	"time"
)

const DatasourceGo = "go"

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
	Kind           string // "repos_added", "repos_removed", "push"
	Repo           string
	// push only
	Ref   string
	Files []string
	// Attempts counts failed handling attempts; NotBefore delays the next one.
	Attempts  int
	NotBefore time.Time
}
