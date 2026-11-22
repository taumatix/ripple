package model

import (
	"strconv"
	"strings"

	"golang.org/x/mod/module"
)

// Line is the key that groups the major versions of one package: the module paths
// example.com/m, example.com/m/v2 and example.com/m/v3 (and gopkg.in/m.v1, .v2)
// are distinct modules to the Go tooling and to the feed, but one line for a human.
// The reverse index is keyed by it, so a v3 release reaches the repos still on v2.
// Outside Go the line is the package itself.
func Line(datasource, pkg string) string {
	if datasource != DatasourceGo {
		return pkg
	}
	prefix, _, ok := module.SplitPathVersion(pkg)
	if !ok {
		return pkg
	}
	return prefix
}

// PathMajor is the major version a Go module path claims: 1 without a suffix
// (which also covers v0), N for /vN or gopkg.in's .vN.
func PathMajor(datasource, pkg string) int {
	if datasource != DatasourceGo {
		return 1
	}
	_, pm, ok := module.SplitPathVersion(pkg)
	if !ok || pm == "" {
		return 1
	}
	n, err := strconv.Atoi(strings.TrimLeft(pm, "/.v"))
	if err != nil {
		return 1
	}
	return n
}
