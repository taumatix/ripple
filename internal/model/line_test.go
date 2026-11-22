package model

import "testing"

func TestLinePathMajor(t *testing.T) {
	for _, c := range []struct {
		pkg   string
		line  string
		major int
	}{
		{"github.com/google/go-github/v60", "github.com/google/go-github", 60},
		{"github.com/google/go-github/v61", "github.com/google/go-github", 61},
		{"github.com/google/go-github", "github.com/google/go-github", 1},
		{"github.com/google/go-github/v2", "github.com/google/go-github", 2},
		{"gopkg.in/yaml.v2", "gopkg.in/yaml", 2},
		{"gopkg.in/yaml.v3", "gopkg.in/yaml", 3},
		{"gopkg.in/yaml.v1", "gopkg.in/yaml", 1},
		{"gopkg.in/natefinch/lumberjack.v2", "gopkg.in/natefinch/lumberjack", 2},
		{"example.com/lib", "example.com/lib", 1},
		{"example.com/v2ray/core", "example.com/v2ray/core", 1},
		{"example.com/api/v1", "example.com/api/v1", 1}, // /v1 is not a valid suffix: part of the name
	} {
		if got := Line(DatasourceGo, c.pkg); got != c.line {
			t.Errorf("Line(%s) = %s, want %s", c.pkg, got, c.line)
		}
		if got := PathMajor(DatasourceGo, c.pkg); got != c.major {
			t.Errorf("PathMajor(%s) = %d, want %d", c.pkg, got, c.major)
		}
	}
	if Line("npm", "left-pad/v2") != "left-pad/v2" {
		t.Error("non-Go packages are their own line")
	}
}
