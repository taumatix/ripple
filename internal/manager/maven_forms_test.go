package manager

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

// The forms below are from docs/vendor/maven/pom-reference.txt:
//   - "Properties": a `<properties><someVar>value</someVar></properties>` value is used as ${someVar},
//     and the properties of a profile are a different set;
//   - "Version Order Specification": trailing zeros are trimmed ("1.0 -> 1", "1.0.0 -> 1"), so 4.13 and
//     4.13.0 are the same version and 4.13 is a plain release, not a qualified one.

func pomWith(props, deps string) string {
	return `<project>
  <modelVersion>4.0.0</modelVersion>
` + props + `  <dependencies>
` + deps + `  </dependencies>
</project>
`
}

func dep(group, artifact, version string) string {
	return "    <dependency>\n      <groupId>" + group + "</groupId>\n      <artifactId>" + artifact + "</artifactId>\n      <version>" + version + "</version>\n    </dependency>\n"
}

func extractMaven(t *testing.T, pom string) []string {
	t.Helper()
	deps, err := Maven{}.Extract("pom.xml", []byte(pom))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range deps {
		got = append(got, d.Package+"@"+d.Version)
	}
	return got
}

func applyMaven(t *testing.T, pom, pkg, from, to string) (string, error) {
	t.Helper()
	out, err := Maven{}.Apply([]byte(pom), model.Dependency{File: "pom.xml", Package: pkg, Version: from}, "", to)
	return string(out), err
}

func TestMavenTwoSegmentVersion(t *testing.T) {
	pom := pomWith("", dep("junit", "junit", "4.13"))
	if got := extractMaven(t, pom); strings.Join(got, " ") != "junit:junit@4.13" {
		t.Fatalf("got %v", got)
	}
	out, err := applyMaven(t, pom, "junit:junit", "4.13", "4.13.2")
	if err != nil || out != strings.Replace(pom, "<version>4.13</version>", "<version>4.13.2</version>", 1) {
		t.Errorf("%v\n%s", err, out)
	}
	if out, err = applyMaven(t, pom, "junit:junit", "4.13", "4.14"); err != nil || !strings.Contains(out, "<version>4.14</version>") {
		t.Errorf("two segments to two: %v\n%s", err, out)
	}
}

func TestMavenOneAndFourSegmentVersionsStayUnread(t *testing.T) {
	pom := pomWith("", dep("a.b", "one", "4")+dep("a.b", "four", "1.2.3.4")+dep("a.b", "qual", "4.13-beta")+dep("a.b", "dot", "4."))
	if got := extractMaven(t, pom); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

func TestMavenPropertyVersion(t *testing.T) {
	props := "  <properties>\n    <junit.version>4.13</junit.version>\n  </properties>\n"
	pom := pomWith(props, dep("junit", "junit", "${junit.version}"))
	if got := extractMaven(t, pom); strings.Join(got, " ") != "junit:junit@4.13" {
		t.Fatalf("got %v", got)
	}
	out, err := applyMaven(t, pom, "junit:junit", "4.13", "4.13.2")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(pom, "<junit.version>4.13<", "<junit.version>4.13.2<", 1); out != want {
		t.Errorf("only the property's value moves, the dependency keeps ${junit.version}:\n%s", out)
	}
}

func TestMavenPropertyUsedInManagementAndDependencies(t *testing.T) {
	props := "  <properties><x.version>3.12.0</x.version></properties>\n"
	pom := `<project>
` + props + `  <dependencyManagement><dependencies>
` + dep("org.apache.commons", "commons-lang3", "${x.version}") + `  </dependencies></dependencyManagement>
  <dependencies>
` + dep("org.apache.commons", "commons-lang3", "${x.version}") + `  </dependencies>
</project>
`
	if got := extractMaven(t, pom); strings.Join(got, " ") != "org.apache.commons:commons-lang3@3.12.0" {
		t.Fatalf("one dependency for the one package: %v", got)
	}
	out, err := applyMaven(t, pom, "org.apache.commons:commons-lang3", "3.12.0", "3.14.0")
	if err != nil || strings.Count(out, "3.14.0") != 1 || strings.Contains(out, "3.12.0") {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestMavenPropertyAndLiteralOfOnePackageMoveTogether(t *testing.T) {
	props := "  <properties><x.version>3.12.0</x.version></properties>\n"
	pom := pomWith(props, dep("a.b", "c", "${x.version}")+dep("a.b", "c", "3.12.0"))
	out, err := applyMaven(t, pom, "a.b:c", "3.12.0", "3.13.0")
	if err != nil || strings.Contains(out, "3.12.0") || strings.Count(out, "3.13.0") != 2 {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestMavenPropertyLeftAlone(t *testing.T) {
	use := dep("a.b", "c", "${v}")
	for name, pom := range map[string]string{
		"used by another package too":             pomWith("<properties><v>1.2.3</v></properties>\n", use+dep("a.b", "other", "${v}")),
		"used by the same group's other artifact": pomWith("<properties><v>1.2.3</v></properties>\n", use+dep("a.b", "sibling", "${v}")),
		"used by a plugin version": `<project><properties><v>1.2.3</v></properties>
<build><plugins><plugin><groupId>p</groupId><artifactId>q</artifactId><version>${v}</version></plugin></plugins></build>
<dependencies>` + use + `</dependencies></project>`,
		"used inside other text":   strings.Replace(pomWith("<properties><v>1.2.3</v></properties>\n", use), "</project>", "<description>built with ${v}</description></project>", 1),
		"used in another property": pomWith("<properties><v>1.2.3</v><w>${v}</w></properties>\n", use),
		"used with a suffix":       pomWith("<properties><v>1.2.3</v></properties>\n", dep("a.b", "c", "${v}-x")),
		"defined twice":            pomWith("<properties><v>1.2.3</v><v>1.2.4</v></properties>\n", use),
		"defined in a profile": `<project><profiles><profile><id>p</id><properties><v>1.2.3</v></properties></profile></profiles>
<dependencies>` + use + `</dependencies></project>`,
		"also defined in a profile": `<project><properties><v>1.2.3</v></properties><profiles><profile><id>p</id><properties><v>9.9.9</v></properties></profile></profiles>
<dependencies>` + use + `</dependencies></project>`,
		"not defined here (a parent's)": pomWith("", use),
		"defined by a comment only":     pomWith("<!-- <properties><v>1.2.3</v></properties> -->\n", use),
		"value is a range":              pomWith("<properties><v>[1.0,2.0)</v></properties>\n", use),
		"value is a property":           pomWith("<properties><v>${w}</v><w>1.2.3</w></properties>\n", use),
		"value is qualified":            pomWith("<properties><v>1.2.3-SNAPSHOT</v></properties>\n", use),
		"project property":              pomWith("<properties><project.v>1.2.3</project.v></properties>\n", dep("a.b", "c", "${project.v}")),
		"env property":                  pomWith("<properties><env.V>1.2.3</env.V></properties>\n", dep("a.b", "c", "${env.V}")),
		"settings property":             pomWith("<properties><settings.v>1.2.3</settings.v></properties>\n", dep("a.b", "c", "${settings.v}")),
		"a cdata use":                   strings.Replace(pomWith("<properties><v>1.2.3</v></properties>\n", use), "</project>", "<description><![CDATA[ ${v} ]]></description></project>", 1),
	} {
		t.Run(name, func(t *testing.T) {
			got := extractMaven(t, pom)
			for _, g := range got {
				if strings.HasPrefix(g, "a.b:c@") {
					t.Errorf("a.b:c must not be tracked: %v", got)
				}
			}
			if _, err := applyMaven(t, pom, "a.b:c", "1.2.3", "1.2.4"); !errors.Is(err, ErrStale) {
				t.Errorf("Apply must find nothing to edit: %v", err)
			}
		})
	}
}

func TestMavenPropertyWhitespaceAndMalformedXML(t *testing.T) {
	pom := pomWith("<properties>\n  <v>\n    2.0.0\n  </v>\n</properties>\n", dep("a.b", "c", " ${v} "))
	if got := extractMaven(t, pom); strings.Join(got, " ") != "a.b:c@2.0.0" {
		t.Fatalf("got %v", got)
	}
	out, err := applyMaven(t, pom, "a.b:c", "2.0.0", "2.1.0")
	if err != nil || !strings.Contains(out, "\n    2.1.0\n  </v>") {
		t.Errorf("whitespace around the value is kept: %v\n%s", err, out)
	}
	// A pom that is not well formed XML keeps the old, literal-only reading and never panics.
	broken := pomWith("<properties><v>2.0.0</properties>\n", dep("a.b", "c", "${v}")+dep("a.b", "d", "1.0.0"))
	if got := extractMaven(t, broken); strings.Join(got, " ") != "a.b:d@1.0.0" {
		t.Errorf("got %v", got)
	}
}

func TestMavenApplyRefusesAChangedPropertyUse(t *testing.T) {
	pom := pomWith("<properties><v>1.2.3</v></properties>\n", dep("a.b", "c", "${v}"))
	if _, err := applyMaven(t, pom, "a.b:c", "1.2.3", "1.2.4"); err != nil {
		t.Fatal(err)
	}
	// A second package begins to use the property after the poll: the job is stale.
	changed := strings.Replace(pom, "</dependencies>", dep("a.b", "other", "${v}")+"</dependencies>", 1)
	if _, err := applyMaven(t, changed, "a.b:c", "1.2.3", "1.2.4"); !errors.Is(err, ErrStale) {
		t.Errorf("got %v", err)
	}
}

// The real-tool fixture (testdata/realtool/maven) is read as TestRealtoolMaven expects, so a run
// without mvn still catches a fixture the manager no longer understands.
func TestMavenRealtoolFixtureIsRead(t *testing.T) {
	b, err := os.ReadFile("testdata/realtool/maven/pom.xml")
	if err != nil {
		t.Fatal(err)
	}
	got := extractMaven(t, string(b))
	want := "org.slf4j:slf4j-api@2.0.0 org.apache.commons:commons-lang3@3.12.0 junit:junit@4.13" // the property is declared first
	if strings.Join(got, " ") != want {
		t.Errorf("got %v, want %q", got, want)
	}
}
