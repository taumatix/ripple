// Package rules decides whether, when and how a detected update is applied.
package rules

import (
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/glob"
)

type UpdateType string

const (
	Major UpdateType = "major"
	Minor UpdateType = "minor"
	Patch UpdateType = "patch"
)

func (t UpdateType) rank() int {
	switch t {
	case Major:
		return 3
	case Minor:
		return 2
	}
	return 1
}

// Max returns the more significant of two update types.
func Max(a, b UpdateType) UpdateType {
	if b.rank() > a.rank() {
		return b
	}
	return a
}

// Classify reports the update type from cur to next, and false when next is not an
// update ripple should propose: not newer, pseudo-version, or a prerelease while
// cur is a release. A current pseudo-version is never updated in v0.1.
func Classify(cur, next string) (UpdateType, bool) {
	if !semver.IsValid(cur) || !semver.IsValid(next) || module.IsPseudoVersion(cur) || module.IsPseudoVersion(next) {
		return "", false
	}
	if semver.Prerelease(next) != "" && semver.Prerelease(cur) == "" {
		return "", false
	}
	if semver.Compare(next, cur) <= 0 {
		return "", false
	}
	switch {
	case semver.Major(cur) != semver.Major(next):
		return Major, true
	case semver.MajorMinor(cur) != semver.MajorMinor(next):
		return Minor, true
	}
	return Patch, true
}

// Limits are the server-wide settings a repo file can tighten but never widen.
type Limits struct {
	MinimumReleaseAge time.Duration
	OpenPRLimit       int
	AllowAutomerge    bool
}

// Decision is the effective policy for one update.
type Decision struct {
	Enabled   bool
	NotBefore time.Time
	Automerge bool
	Group     string
}

func hasType(types []string, t UpdateType) bool {
	if len(types) == 0 {
		return true
	}
	for _, x := range types {
		if UpdateType(x) == t {
			return true
		}
	}
	return false
}

// EffectiveCooldown is the longer of the server minimum and the repo's cooldown.
func EffectiveCooldown(l Limits, u *config.Update) time.Duration {
	if u.Cooldown != nil && u.Cooldown.Duration > l.MinimumReleaseAge {
		return u.Cooldown.Duration
	}
	return l.MinimumReleaseAge
}

// EffectivePRLimit is the smaller of the server limit and the repo's.
func EffectivePRLimit(l Limits, r *config.Repo) int {
	if r.OpenPRLimit > 0 && r.OpenPRLimit < l.OpenPRLimit {
		return r.OpenPRLimit
	}
	return l.OpenPRLimit
}

// Evaluate applies one update entry to a detected update: ignore, cooldown,
// schedule, group, automerge. There is no precedence between entries; the caller
// picks the single entry that governs the dependency. The update is due when
// NotBefore is not after now.
func Evaluate(l Limits, u *config.Update, pkg string, t UpdateType, releasedAt, now time.Time) Decision {
	d := Decision{Enabled: true}
	for _, ig := range u.Ignore {
		if glob.Match(ig.Dependency, pkg) && hasType(ig.UpdateTypes, t) {
			d.Enabled = false
			return d
		}
	}
	for _, g := range u.Groups {
		for _, p := range g.Patterns {
			if glob.Match(p, pkg) && hasType(g.UpdateTypes, t) {
				d.Group = g.Name
				break
			}
		}
		if d.Group != "" {
			break
		}
	}
	if l.AllowAutomerge {
		switch u.Automerge {
		case "patch":
			d.Automerge = t == Patch
		case "minor":
			d.Automerge = t != Major
		case "major":
			d.Automerge = true
		}
	}
	at := releasedAt.Add(EffectiveCooldown(l, u))
	if at.Before(now) {
		at = now
	}
	d.NotBefore = NextInWindow(u.Schedule, at)
	return d
}

// EvaluateSecurity is Evaluate for an update that fixes a published advisory: the
// cooldown, server minimum and repo cooldown alike, is skipped, and nothing else. The
// repo's ignore rules, schedule, groups and automerge setting still decide, except that an
// entry with security-bypass: [schedule] has no schedule for a security update.
func EvaluateSecurity(l Limits, u *config.Update, pkg string, t UpdateType, now time.Time) Decision {
	return EvaluateSecuritySeverity(l, u, pkg, t, now, "")
}

// EvaluateSecuritySeverity is EvaluateSecurity for an advisory with the given rating, so
// that security-bypass-severity can restrict the schedule bypass to serious advisories.
func EvaluateSecuritySeverity(l Limits, u *config.Update, pkg string, t UpdateType, now time.Time, severity string) Decision {
	l.MinimumReleaseAge = 0
	uu := *u
	uu.Cooldown = nil
	if u.BypassesSeverity(config.BypassSchedule, severity) {
		uu.Schedule = nil
	}
	return Evaluate(l, &uu, pkg, t, now, now)
}

var weekdays = map[time.Weekday]string{time.Monday: "mon", time.Tuesday: "tue", time.Wednesday: "wed", time.Thursday: "thu", time.Friday: "fri", time.Saturday: "sat", time.Sunday: "sun"}

// InWindow reports whether t falls inside the schedule. The days are matched
// against the local date of t, also for windows that wrap past midnight.
func InWindow(s *config.Schedule, t time.Time) bool {
	if s == nil {
		return true
	}
	loc := time.UTC
	if s.Timezone != "" {
		if l, err := time.LoadLocation(s.Timezone); err == nil {
			loc = l
		}
	}
	lt := t.In(loc)
	if len(s.Days) > 0 {
		ok := false
		for _, d := range s.Days {
			if d == weekdays[lt.Weekday()] {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	if s.From == "" {
		return true
	}
	cur := lt.Format("15:04")
	if s.From <= s.To {
		return cur >= s.From && cur < s.To
	}
	return cur >= s.From || cur < s.To
}

// NextInWindow returns t when inside the schedule, else the first minute that is.
// A schedule that never opens (not possible after validation) returns t + 8 days.
func NextInWindow(s *config.Schedule, t time.Time) time.Time {
	if InWindow(s, t) {
		return t
	}
	c := t.Truncate(time.Minute)
	for i := 0; i < 8*24*60; i++ {
		c = c.Add(time.Minute)
		if InWindow(s, c) {
			return c
		}
	}
	return t.Add(8 * 24 * time.Hour)
}
