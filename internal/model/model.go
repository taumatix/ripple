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
	// Config is the raw .github/ripple.yml, empty when absent.
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
