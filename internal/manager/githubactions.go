package manager

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
)

// GitHubActions moves the `uses: owner/repo@ref` references of workflows
// (.github/workflows/*.yml|yaml) and composite actions (action.yml|yaml).
//
// A reference is tracked when its ref is a version tag (v4, v4.1.2), or a full commit
// SHA followed by a `# v4.1.2` comment naming the tag it pins. Branch refs, local
// paths (./), docker:// images and a SHA with no version comment are left alone.
// Files are edited line by line, so formatting, quoting and comments survive.
//
// The dependency's Package is owner/repo (a path inside the repo, as in a reusable
// workflow, is not part of it) and its Version the tag, whichever way it is written.
// Apply takes the new tag, or "tag@sha" when the caller has resolved the commit the
// tag points at. Tag references take the tag; SHA references take the commit and the
// tag in their comment, and are an error when no commit was given: a SHA is never
// guessed.
type GitHubActions struct {
	Dir string
}

func (GitHubActions) Name() string { return "github-actions" }

func (g GitHubActions) Matches(p string) bool {
	if !config.InDirectory(g.Dir, p) {
		return false
	}
	switch path.Base(p) {
	case "action.yml", "action.yaml":
		return true
	}
	ext := path.Ext(p)
	return path.Dir(p) == ".github/workflows" && (ext == ".yml" || ext == ".yaml")
}

// usesRE captures: 1 the text up to the value, 2 the opening quote, 3 owner/repo
// with any path, 4 the ref, 5 text between the ref and a comment, 6 the comment's
// first word.
var usesRE = regexp.MustCompile(`^(\s*(?:-\s+)?uses:\s*)(["']?)([^\s"'#@]+)@([^\s"'#]+)(["']?[ \t]*(?:#[ \t]*(\S*))?)`)

var (
	actionTagRE = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+){0,2}$`)
	actionSHARE = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// actionRef classifies one line: the repo, the tag it tracks, and whether it is a
// SHA pin.
func actionRef(m []string) (repo, tag string, pinned, ok bool) {
	spec := m[3]
	if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "docker:") {
		return "", "", false, false
	}
	parts := strings.Split(spec, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false, false
	}
	repo = parts[0] + "/" + parts[1]
	switch {
	case actionTagRE.MatchString(m[4]):
		return repo, m[4], false, true
	case actionSHARE.MatchString(m[4]) && actionTagRE.MatchString(m[6]):
		return repo, m[6], true, true
	}
	return "", "", false, false
}

func (g GitHubActions) Extract(p string, content []byte) ([]model.Dependency, error) {
	var out []model.Dependency
	seen := map[model.Dependency]bool{}
	for _, line := range strings.Split(string(content), "\n") {
		m := usesRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if repo, tag, _, ok := actionRef(m); ok {
			d := model.Dependency{File: p, Manager: g.Name(), Datasource: model.DatasourceGitHubTags, Package: repo, Version: tag}
			// Apply rewrites every line naming the pair, so it is one dependency.
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out, nil
}

func (g GitHubActions) Apply(content []byte, dep model.Dependency, newPackage, newVersion string) ([]byte, error) {
	if newPackage != "" && newPackage != dep.Package {
		return nil, fmt.Errorf("%w: a GitHub Action cannot be renamed", ErrUnsupported)
	}
	tag, sha, haveSHA := strings.Cut(newVersion, "@")
	lines := strings.Split(string(content), "\n")
	changed := false
	for i, line := range lines {
		m := usesRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		repo, old, pinned, ok := actionRef(m)
		if !ok || repo != dep.Package || old != dep.Version {
			continue
		}
		if pinned && !haveSHA {
			return nil, fmt.Errorf("github-actions: %s@%s is pinned to a commit and none was resolved for %s", dep.Package, dep.Version, tag)
		}
		start := len(m[1]) + len(m[2]) + len(m[3]) + 1 // the ref
		end := start + len(m[4])
		repl := tag
		rest := line[end:]
		if pinned {
			repl = sha
			// rest begins with the closing quote and spaces, then "#", spaces, the old tag
			c := strings.Index(rest, "#")
			w := c + 1 + len(rest[c+1:]) - len(strings.TrimLeft(rest[c+1:], " \t"))
			rest = rest[:w] + tag + rest[w+len(old):]
		}
		lines[i] = line[:start] + repl + rest
		changed = true
	}
	if !changed {
		return nil, fmt.Errorf("github-actions: %s@%s not found", dep.Package, dep.Version)
	}
	out := []byte(strings.Join(lines, "\n"))
	// A document that parsed before must still parse: the one thing a line edit can
	// break is a ref that is not a plain scalar.
	var doc any
	if yaml.Unmarshal(content, &doc) == nil {
		if err := yaml.Unmarshal(out, &doc); err != nil {
			return nil, fmt.Errorf("github-actions: rewriting %s@%s makes the file invalid YAML: %w", dep.Package, dep.Version, err)
		}
	}
	return out, nil
}
