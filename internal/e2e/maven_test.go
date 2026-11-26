package e2e

import (
	"reflect"
	"sort"
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

// pomForms mixes the forms that are read since ROADMAP 239's first piece: a two-segment version, a
// version given by a property of the pom's own <properties>, and the ones that stay unread (a
// property used by two packages, a property the pom does not define).
const pomForms = `<project>
  <modelVersion>4.0.0</modelVersion>
  <properties>
    <commons.version>3.12.0</commons.version>
    <shared.version>2.0.0</shared.version>
  </properties>
  <dependencies>
    <dependency>
      <groupId>junit</groupId>
      <artifactId>junit</artifactId>
      <version>4.13</version>
    </dependency>
    <dependency>
      <groupId>a.b</groupId>
      <artifactId>short</artifactId>
      <version>1.0</version>
    </dependency>
    <dependency>
      <groupId>org.apache.commons</groupId>
      <artifactId>commons-lang3</artifactId>
      <version>${commons.version}</version>
    </dependency>
    <dependency>
      <groupId>com.google.guava</groupId>
      <artifactId>guava</artifactId>
      <version>32.1.2</version>
    </dependency>
    <dependency>
      <groupId>org.slf4j</groupId>
      <artifactId>slf4j-api</artifactId>
      <version>${shared.version}</version>
    </dependency>
    <dependency>
      <groupId>org.slf4j</groupId>
      <artifactId>slf4j-simple</artifactId>
      <version>${shared.version}</version>
    </dependency>
    <dependency>
      <groupId>a.b</groupId>
      <artifactId>parented</artifactId>
      <version>${parent.managed.version}</version>
    </dependency>
  </dependencies>
</project>
`

func TestMavenTwoSegmentAndPropertyVersions(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := mavenEnv(t, st)
		reg.Publish("junit:junit", "4.13")
		reg.Publish("a.b:short", "1.0")
		reg.Publish("org.apache.commons:commons-lang3", "3.12.0")
		reg.Publish("com.google.guava:guava", "32.1.2")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": mavenConfig, "pom.xml": pomForms})
		start(t, e, "acme/app")
		var got []string
		for _, d := range e.deps("acme/app") {
			got = append(got, d.Package+"@"+d.Version)
		}
		sort.Strings(got)
		want := []string{"a.b:short@1.0", "com.google.guava:guava@32.1.2", "junit:junit@4.13", "org.apache.commons:commons-lang3@3.12.0"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("tracked %v, want %v (a property shared by two packages and one the pom does not define stay unread)", got, want)
		}

		e.clock.Advance(time.Minute)
		reg.Publish("junit:junit", "4.13.2")
		reg.Publish("junit:junit", "4.13.3-SNAPSHOT") // qualified: not proposed
		reg.Publish("a.b:short", "1.1")               // a two-segment declaration moves to a two-segment release
		reg.Publish("org.apache.commons:commons-lang3", "3.14.0")
		reg.Publish("com.google.guava:guava", "33.1") // a three-segment declaration never moves to a two-segment one
		reg.Publish("com.google.guava:guava", "33.0.0")
		reg.Publish("org.slf4j:slf4j-api", "2.0.9")
		reg.Publish("org.slf4j:slf4j-simple", "2.0.9")
		e.tick()
		e.tick()

		edits := map[string][2]string{
			"junit":         {"<version>4.13</version>", "<version>4.13.2</version>"},
			"short":         {"<version>1.0</version>", "<version>1.1</version>"},
			"commons-lang3": {"<commons.version>3.12.0<", "<commons.version>3.14.0<"}, // the property moves, the dependency keeps ${commons.version}
			"guava":         {"<version>32.1.2</version>", "<version>33.0.0</version>"},
		}
		prs := e.gh.PRs("acme/app")
		if len(prs) != len(edits) {
			t.Fatalf("one PR per tracked package, got %d", len(prs))
		}
		for _, pr := range prs {
			content, _ := e.gh.FileAt("acme/app", pr.Head, "pom.xml")
			var edit [2]string
			for name, ed := range edits {
				if strings.Contains(pr.Head, name) {
					edit = ed
				}
			}
			if edit == ([2]string{}) {
				t.Fatalf("unexpected branch %q", pr.Head)
			}
			if want := strings.Replace(pomForms, edit[0], edit[1], 1); content != want {
				t.Errorf("%s: only that text moves, layout kept:\n%s", pr.Head, content)
			}
		}
		for _, r := range reg.Requests() {
			if strings.Contains(r.Path, "slf4j") || strings.Contains(r.Path, "parented") {
				t.Errorf("a package ripple does not track was asked for: %s", r.Path)
			}
		}
	})
}
