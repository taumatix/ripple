package manager

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

var updateCorpus = flag.Bool("update-corpus", false, "rewrite the corpus .want files")

type extractor interface {
	Extract(path string, content []byte) ([]model.Dependency, error)
}

// TestCorpus reads manifests written the way real projects write them (ripgrep, tokio,
// express, bitnami and ingress-nginx charts, ...) and compares what each manager extracts
// with a reviewed .want file beside them: "package : version" per dependency, in order.
// A change in what is read, in either direction, fails here and must be reviewed.
func TestCorpus(t *testing.T) {
	cargo, npm, docker := Cargo{Dir: "/"}, Npm{Dir: "/"}, Docker{Dir: "/"}
	cases := []struct {
		file, as string
		m        extractor
	}{
		{"ripgrep.Cargo.toml", "Cargo.toml", cargo},
		{"tokio.Cargo.toml", "Cargo.toml", cargo},
		{"workspace.Cargo.toml", "Cargo.toml", cargo},
		{"member.Cargo.toml", "Cargo.toml", cargo},
		{"express.package.json", "package.json", npm},
		{"app.package.json", "package.json", npm},
		{"node.Dockerfile", "Dockerfile", docker},
		{"docker-compose.yml", "docker-compose.yml", docker},
		{"redis.values.yaml", "values.yaml", docker},
		{"ingress.values.yaml", "values.yaml", docker},
		{"kustomization.yaml", "kustomization.yaml", docker},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			path := filepath.Join("testdata", "corpus", c.file)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			deps, err := c.m.Extract(c.as, b)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			var sb strings.Builder
			for _, d := range deps {
				fmt.Fprintf(&sb, "%s : %s\n", d.Package, d.Version)
			}
			if *updateCorpus {
				if err := os.WriteFile(path+".want", []byte(sb.String()), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path + ".want")
			if err != nil {
				t.Fatal(err)
			}
			if got := sb.String(); got != string(want) {
				t.Errorf("extracted:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// TestCorpusDockerApply edits the corpus files and checks that only the intended line moved,
// with comments, quoting and anchors left alone.
func TestCorpusDockerApply(t *testing.T) {
	d := Docker{Dir: "/"}
	read := func(f string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", "corpus", f))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	cases := []struct {
		file, as, pkg, from, to string
		old, new                string
	}{
		{"docker-compose.yml", "docker-compose.yml", "docker.io/library/nginx", "1.27.0", "1.27.1",
			"image: nginx:1.27.0 # edge", "image: nginx:1.27.1 # edge"},
		{"docker-compose.yml", "docker-compose.yml", "docker.io/library/redis", "7.2", "7.4",
			`image: "redis:7.2"`, `image: "redis:7.4"`},
		{"redis.values.yaml", "values.yaml", "docker.io/bitnami/redis", "7.2.5-debian-12-r4", "7.2.6-debian-12-r0",
			"tag: 7.2.5-debian-12-r4", "tag: 7.2.6-debian-12-r0"},
		{"kustomization.yaml", "kustomization.yaml", "docker.io/library/nginx", "1.27", "1.28",
			`newTag: "1.27"`, `newTag: "1.28"`},
		{"node.Dockerfile", "Dockerfile", "ghcr.io/astral-sh/uv", "0.4.9", "0.5.0",
			"COPY --from=ghcr.io/astral-sh/uv:0.4.9 /uv", "COPY --from=ghcr.io/astral-sh/uv:0.5.0 /uv"},
	}
	for _, c := range cases {
		t.Run(c.file+"/"+c.pkg, func(t *testing.T) {
			src := read(c.file)
			dep := model.Dependency{File: c.as, Manager: "docker", Package: c.pkg, Version: c.from}
			out, err := d.Apply(src, dep, c.pkg, c.to)
			if err != nil {
				t.Fatal(err)
			}
			if want := strings.Replace(string(src), c.old, c.new, 1); string(out) != want {
				t.Errorf("Apply changed more or less than %q:\n%s", c.old, out)
			}
		})
	}
}
