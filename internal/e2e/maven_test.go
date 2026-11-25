package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const mavenConfig = "updates:\n  - ecosystem: maven\n"

func mavenEnv(t *testing.T, st store.Store) (*env, *testutil.Maven) {
	m := testutil.NewMaven(t)
	return newEnvOpts(t, st, defaultLimits(), opts{maven: m}), m
}

const pomE2E = `<project>
  <modelVersion>4.0.0</modelVersion>
  <dependencies>
    <dependency>
      <groupId>org.apache.commons</groupId>
      <artifactId>commons-lang3</artifactId>
      <version>3.12.0</version>
    </dependency>
    <dependency>
      <groupId>com.google.guava</groupId>
      <artifactId>guava</artifactId>
      <version>32.1.2</version>
    </dependency>
    <dependency>
      <groupId>org.slf4j</groupId>
      <artifactId>slf4j-api</artifactId>
      <version>${slf4j.version}</version>
    </dependency>
    <dependency>
      <groupId>a.b</groupId>
      <artifactId>ranged</artifactId>
      <version>[1.0,2.0)</version>
    </dependency>
  </dependencies>
</project>
`

func TestMavenPackageBump(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := mavenEnv(t, st)
		reg.Publish("org.apache.commons:commons-lang3", "3.12.0")
		reg.Publish("com.google.guava:guava", "32.1.2")
		reg.Publish("org.slf4j:slf4j-api", "2.0.0")
		reg.Publish("a.b:ranged", "1.0.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": mavenConfig, "svc/pom.xml": pomE2E})
		e.gh.AddRepo("acme/optout", map[string]string{"svc/pom.xml": pomE2E})
		start(t, e, "acme/app", "acme/optout")
		if n := len(e.deps("acme/app")); n != 2 {
			t.Fatalf("commons-lang3 and guava are tracked (not the property or the range), got %+v", e.deps("acme/app"))
		}

		e.clock.Advance(time.Minute)
		reg.Publish("org.apache.commons:commons-lang3", "3.14.0")
		reg.Publish("org.apache.commons:commons-lang3", "3.15.0-SNAPSHOT") // qualified: not proposed
		reg.Publish("com.google.guava:guava", "33.0.0")
		reg.Publish("com.google.guava:guava", "33.0") // two segments: not proposed
		reg.Publish("org.slf4j:slf4j-api", "2.0.9")   // never read: a property
		reg.Publish("a.b:ranged", "1.5.0")            // never read: a range
		e.tick()
		e.tick() // a replay must not open a second PR

		prs := e.gh.PRs("acme/app")
		if len(prs) != 2 {
			t.Fatalf("one PR per artifact, got %d", len(prs))
		}
		for _, pr := range prs {
			got, _ := e.gh.FileAt("acme/app", pr.Head, "svc/pom.xml")
			var from, to string
			switch {
			case strings.Contains(pr.Head, "commons-lang3"):
				from, to = "<version>3.12.0</version>", "<version>3.14.0</version>"
			case strings.Contains(pr.Head, "guava"):
				from, to = "<version>32.1.2</version>", "<version>33.0.0</version>"
			default:
				t.Fatalf("unexpected branch %q", pr.Head)
			}
			if want := strings.Replace(pomE2E, from, to, 1); got != want {
				t.Errorf("%s: only that version moves, layout kept:\n%s", pr.Head, got)
			}
			if !strings.HasPrefix(pr.Head, "ripple/maven/") {
				t.Errorf("branch %q", pr.Head)
			}
		}
		for _, r := range reg.Requests() {
			if r.Authorization != "" || !strings.HasSuffix(r.Path, "/maven-metadata.xml") {
				t.Errorf("request %+v: anonymous, and only for an artifact's metadata", r)
			}
			if strings.Contains(r.Path, "slf4j") || strings.Contains(r.Path, "ranged") {
				t.Errorf("a dependency ripple does not track was asked for: %s", r.Path)
			}
		}
		if len(e.gh.PRs("acme/optout")) != 0 || len(e.deps("acme/optout")) != 0 {
			t.Error("a repo that did not opt in was touched")
		}
	})
}

func TestMavenPrivateRepositoryIsNotRead(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := mavenEnv(t, st)
		reg.Publish("org.apache.commons:commons-lang3", "3.12.0")
		pom := strings.Replace(pomE2E, "<dependencies>", "<repositories><repository><id>p</id><url>https://repo.example/m2</url></repository></repositories>\n  <dependencies>", 1)
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": mavenConfig, "pom.xml": pom})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		reg.Publish("org.apache.commons:commons-lang3", "3.14.0")
		e.tick()
		if len(e.deps("acme/app")) != 0 || len(e.gh.PRs("acme/app")) != 0 || len(reg.Requests()) != 0 {
			t.Errorf("a pom with its own repositories must be left alone: deps %v, requests %v", e.deps("acme/app"), reg.Requests())
		}
	})
}

func TestMavenDeniedArtifactIsLeftAlone(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := mavenEnv(t, st)
		reg.Publish("org.apache.commons:commons-lang3", "3.12.0")
		reg.Deny("org.apache.commons:commons-lang3")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": mavenConfig, "pom.xml": pomE2E})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Errorf("an artifact that refuses anonymous reads opened %d PRs", n)
		}
	})
}
