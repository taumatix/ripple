package manager

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

const mainTF = `# module "commented" { source = "a/b/c" version = "9.9.9" }
terraform {
  required_version = ">= 1.5.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.1.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "= 3.5.1"
    }
    ranged = {
      source  = "acme/ranged"
      version = ">= 1.0.0, < 2.0.0"
    }
    twoseg = {
      source  = "acme/twoseg"
      version = "~> 1.2"
    }
    legacy = "~> 4.0"
    hosted = {
      source  = "example.com/acme/hosted"
      version = "1.0.0"
    }
    nosrc = {
      version = "1.0.0"
    }
  }
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.1.0"
  name    = "${var.name}"
  tags    = { version = "9.9.9" }
}

module "local" {
  source = "./modules/x"
}

module "git" {
  source  = "git::https://example.com/x.git"
  version = "1.0.0"
}

module "interp" {
  source  = "acme/interp/aws"
  version = "${var.v}"
}

module "again" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.1.0"
  user_data = <<-EOT
    module "heredoc" {
      source = "x/y/z"
      version = "1.1.1"
    }
  EOT
}

/* module "block" {
  source = "a/b/c"
  version = "2.2.2"
} */
`

func TestTerraformExtract(t *testing.T) {
	deps, err := Terraform{}.Extract("main.tf", []byte(mainTF))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range deps {
		if d.Datasource != model.DatasourceTerraform || d.Manager != "terraform" || d.File != "main.tf" {
			t.Errorf("dep %+v", d)
		}
		got = append(got, d.Package+"@"+d.Version)
	}
	want := []string{"providers/hashicorp/aws@5.1.0", "providers/hashicorp/random@3.5.1", "modules/terraform-aws-modules/vpc/aws@5.1.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestTerraformApply(t *testing.T) {
	tf := Terraform{}
	deps, _ := tf.Extract("main.tf", []byte(mainTF))
	byPkg := map[string]model.Dependency{}
	for _, d := range deps {
		byPkg[d.Package] = d
	}
	out, err := tf.Apply([]byte(mainTF), byPkg["modules/terraform-aws-modules/vpc/aws"], "", "v5.2.0")
	if err != nil {
		t.Fatal(err)
	}
	// Both modules using the package move; nothing else changes, byte for byte.
	want := strings.Replace(mainTF, `version = "5.1.0"`, `version = "5.2.0"`, 2)
	want = strings.Replace(want, `version = "~> 5.2.0"`, `version = "~> 5.1.0"`, 1)
	if string(out) != want {
		t.Errorf("diff:\n%s", out)
	}
	out, err = tf.Apply([]byte(mainTF), byPkg["providers/hashicorp/aws"], "", "5.9.0")
	if err != nil || string(out) != strings.Replace(mainTF, `"~> 5.1.0"`, `"~> 5.9.0"`, 1) {
		t.Errorf("provider: %v\n%s", err, out)
	}
	out, err = tf.Apply([]byte(mainTF), byPkg["providers/hashicorp/random"], "", "3.6.0")
	if err != nil || string(out) != strings.Replace(mainTF, `"= 3.5.1"`, `"= 3.6.0"`, 1) {
		t.Errorf("= form: %v", err)
	}
	if _, err = tf.Apply([]byte(strings.ReplaceAll(mainTF, "5.1.0", "5.1.1")), byPkg["providers/hashicorp/aws"], "", "5.9.0"); !errors.Is(err, ErrStale) {
		t.Errorf("stale: %v", err)
	}
	if _, err = tf.Apply([]byte(mainTF), byPkg["providers/hashicorp/aws"], "other/pkg", "5.9.0"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("rename: %v", err)
	}
	if _, err = tf.Apply([]byte(mainTF), byPkg["providers/hashicorp/aws"], "", "6.0.0-beta1"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("prerelease: %v", err)
	}
}

func TestTerraformMatches(t *testing.T) {
	tf := Terraform{Dir: "infra"}
	if !tf.Matches("infra/main.tf") || tf.Matches("other/main.tf") || tf.Matches("infra/main.tf.json") || tf.Matches("infra/x.tfvars") {
		t.Error("Matches")
	}
}
