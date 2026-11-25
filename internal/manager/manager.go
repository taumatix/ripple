// Package manager extracts dependencies from, and rewrites, repository files.
package manager

import (
	"fmt"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
)

type Manager interface {
	Name() string
	Matches(filePath string) bool
	// Extract returns the dependencies in content; Repo is left empty.
	Extract(filePath string, content []byte) ([]model.Dependency, error)
	// Apply returns content with dep moved to newVersion. newPackage differs from
	// dep.Package when the update changes the package's name (a Go major version);
	// managers that cannot do that return ErrUnsupported.
	Apply(content []byte, dep model.Dependency, newPackage, newVersion string) ([]byte, error)
}

// Build returns one manager per update entry of cfg, in file order.
func Build(cfg *config.Repo) ([]Manager, error) {
	var out []Manager
	for i, u := range cfg.Updates {
		switch u.Ecosystem {
		case "gomod":
			out = append(out, GoMod{Dir: u.Directory, Toolchain: u.GoToolchain})
		case "github-actions":
			out = append(out, GitHubActions{Dir: u.Directory})
		case "docker":
			out = append(out, Docker{Dir: u.Directory})
		case "npm":
			out = append(out, Npm{Dir: u.Directory})
		case "cargo":
			out = append(out, Cargo{Dir: u.Directory})
		case "helm":
			out = append(out, Helm{Dir: u.Directory})
		case "bundler":
			out = append(out, Bundler{Dir: u.Directory})
		case "composer":
			out = append(out, Composer{Dir: u.Directory})
		case "nuget":
			out = append(out, Nuget{Dir: u.Directory})
		case "pip":
			out = append(out, Pip{Dir: u.Directory})
		case "regex":
			r, err := NewRegex(cfg.ManagerName(i), u)
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		default:
			return nil, fmt.Errorf("unknown ecosystem %q", u.Ecosystem)
		}
	}
	return out, nil
}

// ByName finds a manager by Name.
func ByName(ms []Manager, name string) (Manager, bool) {
	for _, m := range ms {
		if m.Name() == name {
			return m, true
		}
	}
	return nil, false
}

// Relevant reports whether a change to filePath can alter extracted dependencies.
func Relevant(ms []Manager, filePath string) bool {
	if filePath == config.FileName || filePath == config.LegacyFileName {
		return true
	}
	for _, m := range ms {
		if m.Matches(filePath) {
			return true
		}
	}
	return false
}
