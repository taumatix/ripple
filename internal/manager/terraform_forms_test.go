package manager

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

// The forms below are from docs/vendor/terraform/version-constraints.txt:
//   - "Operators", `~>`: "Allows only the right-most version component to increment", with
//     `~> 1.0.4` allowing 1.0.5 and 1.0.10 but not 1.1.0, and `~> 1.1` allowing 1.2 and 1.10 but not 2.0;
//   - "Best practices": a reusable module "should constrain only [its] minimum allowed versions ...
//     such as >= 0.12.0", so a `>=` floor says what the module needs, not which release to use, and
//     raising it to the newest release would change what the module claims to support. `>=`, `>`,
//     `<`, `<=`, `!=` and comma lists therefore stay unread.

func tfProviderFile(constraint string) string {
	return "terraform {\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n      version = \"" + constraint + "\"\n    }\n  }\n}\n"
}

func tfModuleFile(constraint string) string {
	return "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"" + constraint + "\"\n}\n"
}

func extractTF(t *testing.T, src string) []model.Dependency {
	t.Helper()
	deps, err := Terraform{}.Extract("main.tf", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return deps
}

func TestTerraformPessimisticTwoSegments(t *testing.T) {
	// `~> 1.1` allows 1.2 and 1.10 but not 2.0: the shape is "this major, at least this minor".
	for _, c := range []struct{ constraint, newVersion, want string }{
		{"~> 1.1", "1.10.3", "~> 1.10"},
		{"~> 1.1", "2.0.0", "~> 2.0"},
		{"~>1.1", "1.4.0", "~>1.4"},
		{"~>   1.1", "1.4.0", "~>   1.4"},
		{" ~> 1.1 ", "1.4.0", " ~> 1.4 "},
	} {
		for name, src := range map[string]string{"provider": tfProviderFile(c.constraint), "module": tfModuleFile(c.constraint)} {
			deps := extractTF(t, src)
			if len(deps) != 1 || deps[0].Version != "1.1" {
				t.Fatalf("%s %q: got %+v", name, c.constraint, deps)
			}
			out, err := Terraform{}.Apply([]byte(src), deps[0], "", c.newVersion)
			if err != nil || string(out) != strings.Replace(src, c.constraint, c.want, 1) {
				t.Errorf("%s %q -> %s: %v\n%s", name, c.constraint, c.newVersion, err, out)
			}
		}
	}
}

func TestTerraformPessimisticThreeSegmentsStillMoveToThreeSegments(t *testing.T) {
	// `~> 1.0.4` allows 1.0.5 and 1.0.10 but not 1.1.0: nothing about the existing form changes.
	src := tfProviderFile("~> 1.0.4")
	deps := extractTF(t, src)
	if len(deps) != 1 || deps[0].Version != "1.0.4" {
		t.Fatalf("got %+v", deps)
	}
	out, err := Terraform{}.Apply([]byte(src), deps[0], "", "1.1.0")
	if err != nil || string(out) != tfProviderFile("~> 1.1.0") {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestTerraformOperatorsThatStayUnread(t *testing.T) {
	for _, c := range []string{
		">= 1.0", ">= 1.0.0", "> 1.0", "< 2.0", "<= 2.0.0", "!= 1.0.0", ">= 1.0, < 2.0", "~> 1.0, >= 1.0.4", "~> 1", "~> 1.2.3.4",
		"1.2", "= 1.2", "= 1", "1", "~> 1.2-beta", "~> v1.2", "~> 1.", "~> .2", "1.2.0-beta1", "~>",
	} {
		for name, src := range map[string]string{"provider": tfProviderFile(c), "module": tfModuleFile(c)} {
			if deps := extractTF(t, src); len(deps) != 0 {
				t.Errorf("%s %q must stay unread, got %+v", name, c, deps)
			}
		}
	}
}

func TestTerraformApplyKeepsEachDeclarationsShape(t *testing.T) {
	// A two- and a three-segment declaration of one package are different Dependencies; moving one
	// leaves the other alone, byte for byte.
	src := tfModuleFile("~> 5.1.0") + "\n" + strings.Replace(tfModuleFile("~> 5.1"), "\"vpc\"", "\"vpc2\"", 1)
	tf := Terraform{}
	deps := extractTF(t, src)
	if len(deps) != 2 || deps[0].Version != "5.1.0" || deps[1].Version != "5.1" {
		t.Fatalf("got %+v", deps)
	}
	out, err := Terraform{}.Apply([]byte(src), deps[1], "", "5.3.2")
	if err != nil || string(out) != strings.Replace(src, `"~> 5.1"`, `"~> 5.3"`, 1) {
		t.Errorf("%v\n%s", err, out)
	}
	out, err = Terraform{}.Apply([]byte(src), deps[0], "", "5.3.2")
	if err != nil || string(out) != strings.Replace(src, `"~> 5.1.0"`, `"~> 5.3.2"`, 1) {
		t.Errorf("%v\n%s", err, out)
	}
	if _, err := tf.Apply([]byte(tfModuleFile("~> 5.2")), deps[1], "", "5.3.2"); !errors.Is(err, ErrStale) {
		t.Errorf("a changed constraint is stale: %v", err)
	}
	if out, err := tf.Apply([]byte(src), deps[1], "", "5.3"); err != nil || string(out) != strings.Replace(src, `"~> 5.1"`, `"~> 5.3"`, 1) {
		t.Errorf("a two-segment declaration also takes X.Y: %v\n%s", err, out)
	}
	if _, err := tf.Apply([]byte(src), deps[0], "", "5.3"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("a three-segment declaration needs a three-segment release: %v", err)
	}
	if _, err := tf.Apply([]byte(src), deps[1], "", "5.3.2-beta1"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("prerelease: %v", err)
	}
}

func TestParseTerraformDeclared(t *testing.T) {
	for s, want := range map[string]bool{"1.2": true, "1.2.3": true, "10.20.30": true, "1": false, "1.2.3.4": false, "1.2-b": false, "": false, "1.": false} {
		if got := ParseTerraformDeclared(s); got != want {
			t.Errorf("%q: %v", s, got)
		}
	}
	if ParseTerraformSpec("1.2") {
		t.Error("ParseTerraformSpec is the release shape and keeps three segments")
	}
}

// The real-tool fixture (testdata/realtool/terraform) is read as TestRealtoolTerraform expects,
// so a run without terraform still catches a fixture the manager no longer understands.
func TestTerraformRealtoolFixtureIsRead(t *testing.T) {
	b, err := os.ReadFile("testdata/realtool/terraform/main.tf")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range extractTF(t, string(b)) {
		got = append(got, d.Package+"@"+d.Version)
	}
	want := "providers/hashicorp/random@3.5.0 providers/hashicorp/null@3.1 modules/cloudposse/label/null@0.24.1"
	if strings.Join(got, " ") != want {
		t.Errorf("got %v, want %q", got, want)
	}
}
