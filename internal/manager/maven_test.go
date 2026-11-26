package manager

import (
	"errors"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

const pomXML = `<?xml version="1.0" encoding="UTF-8"?>
<project>
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>org.springframework.boot</groupId>
    <artifactId>spring-boot-starter-parent</artifactId>
    <version>3.1.0</version>
  </parent>
  <properties><junit.version>5.9.0</junit.version></properties>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>com.google.guava</groupId>
        <artifactId>guava</artifactId>
        <version>32.1.2</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
  <dependencies>
    <dependency>
      <groupId>org.apache.commons</groupId>
      <artifactId>commons-lang3</artifactId>
      <version>3.12.0</version>
      <exclusions>
        <exclusion><groupId>x.y</groupId><artifactId>z</artifactId></exclusion>
      </exclusions>
    </dependency>
    <dependency><groupId>org.junit</groupId><artifactId>junit</artifactId><version>${junit.version}</version></dependency>
    <dependency><groupId>a.b</groupId><artifactId>ranged</artifactId><version>[1.0,2.0)</version></dependency>
    <dependency><groupId>a.b</groupId><artifactId>snap</artifactId><version>1.0.0-SNAPSHOT</version></dependency>
    <dependency><groupId>a.b</groupId><artifactId>two</artifactId><version>1.2</version></dependency>
    <dependency><groupId>a.b</groupId><artifactId>managed</artifactId></dependency>
    <dependency>
      <groupId>a.b</groupId>
      <artifactId>spaced</artifactId>
      <version> 2.0.0 </version>
    </dependency>
    <!-- <dependency><groupId>a.b</groupId><artifactId>commented</artifactId><version>9.9.9</version></dependency> -->
  </dependencies>
</project>
`

func TestMavenExtract(t *testing.T) {
	deps, err := Maven{}.Extract("pom.xml", []byte(pomXML))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range deps {
		if d.Datasource != model.DatasourceMaven || d.Manager != "maven" {
			t.Errorf("%+v", d)
		}
		got = append(got, d.Package+"@"+d.Version)
	}
	// org.junit:junit is the ${junit.version} property (read from <properties>); a.b:two is `1.2`.
	want := "org.junit:junit@5.9.0 com.google.guava:guava@32.1.2 org.apache.commons:commons-lang3@3.12.0 a.b:two@1.2 a.b:spaced@2.0.0"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestMavenRepositoriesLeaveFileAlone(t *testing.T) {
	for _, tag := range []string{"repositories", "pluginRepositories"} {
		pom := strings.Replace(pomXML, "<properties>", "<"+tag+"><repository><id>x</id><url>https://p.example/</url></repository></"+tag+">\n  <properties>", 1)
		if deps, _ := (Maven{}).Extract("pom.xml", []byte(pom)); len(deps) != 0 {
			t.Errorf("%s: file with a private repository must be ignored, got %v", tag, deps)
		}
	}
	// A repository under distributionManagement is where it publishes, not where it reads.
	pom := strings.Replace(pomXML, "<properties>", "<distributionManagement><repository><id>x</id><url>u</url></repository></distributionManagement>\n  <properties>", 1)
	if deps, _ := (Maven{}).Extract("pom.xml", []byte(pom)); len(deps) != 5 {
		t.Errorf("distributionManagement must not hide the file, got %v", deps)
	}
}

func TestMavenApply(t *testing.T) {
	dep := model.Dependency{File: "pom.xml", Package: "org.apache.commons:commons-lang3", Version: "3.12.0"}
	out, err := Maven{}.Apply([]byte(pomXML), dep, "", "3.14.0")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(pomXML, "<version>3.12.0</version>", "<version>3.14.0</version>", 1); string(out) != want {
		t.Errorf("only the digits move:\n%s", out)
	}
	dep = model.Dependency{File: "pom.xml", Package: "a.b:spaced", Version: "2.0.0"}
	if out, err = (Maven{}).Apply([]byte(pomXML), dep, "", "2.1.0"); err != nil || !strings.Contains(string(out), "<version> 2.1.0 </version>") {
		t.Errorf("spaced: %v\n%s", err, out)
	}
	for _, c := range []struct {
		pkg, ver string
		want     error
	}{
		{"a.b:commented", "9.9.9", ErrStale},
		{"a.b:spaced", "1.0.0", ErrStale},
		{"x.y:z", "1.0.0", ErrStale},
	} {
		if _, err := (Maven{}).Apply([]byte(pomXML), model.Dependency{File: "pom.xml", Package: c.pkg, Version: c.ver}, "", "9.9.10"); !errors.Is(err, c.want) {
			t.Errorf("%s %s: %v", c.pkg, c.ver, err)
		}
	}
	d := model.Dependency{Package: "a.b:spaced", Version: "2.0.0"}
	if _, err := (Maven{}).Apply([]byte(pomXML), d, "other:x", "2.1.0"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("rename: %v", err)
	}
	if _, err := (Maven{}).Apply([]byte(pomXML), d, "", "3.0.0-RC1"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("qualified target: %v", err)
	}
}

func TestMavenMatches(t *testing.T) {
	m := Maven{Dir: "/svc"}
	for p, want := range map[string]bool{"svc/pom.xml": true, "svc/mod/pom.xml": true, "pom.xml": false, "svc/pom.xml.bak": false, "svc/build.gradle": false} {
		if m.Matches(p) != want {
			t.Errorf("%s: want %v", p, want)
		}
	}
}
