package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const terraformConfig = "updates:\n  - ecosystem: terraform\n"

func terraformEnv(t *testing.T, st store.Store) (*env, *testutil.Terraform) {
	r := testutil.NewTerraform(t)
	return newEnvOpts(t, st, defaultLimits(), opts{terraform: r}), r
}

const tfE2E = `terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.1.0"
    }
    ranged = {
      source  = "acme/ranged"
      version = ">= 1.0.0"
    }
  }
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.1.0"
}

module "git" {
  source  = "git::https://example.com/x.git"
  version = "1.0.0"
}
`

func TestTerraformPackageBump(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := terraformEnv(t, st)
		reg.Publish("providers/hashicorp/aws", "4.1.0")
		reg.Publish("modules/terraform-aws-modules/vpc/aws", "5.1.0")
		reg.Publish("providers/acme/ranged", "1.0.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": terraformConfig, "infra/main.tf": tfE2E, "infra/.terraform.lock.hcl": "# lock\n"})
		e.gh.AddRepo("acme/optout", map[string]string{"infra/main.tf": tfE2E})
		start(t, e, "acme/app", "acme/optout")
		if n := len(e.deps("acme/app")); n != 2 {
			t.Fatalf("the provider and the module are tracked (not the range or the git source), got %+v", e.deps("acme/app"))
		}

		e.clock.Advance(time.Minute)
		reg.Publish("providers/hashicorp/aws", "4.2.0")
		reg.Publish("providers/hashicorp/aws", "5.0.0-beta1") // prerelease: not proposed
		reg.Publish("modules/terraform-aws-modules/vpc/aws", "5.2.0")
		reg.Publish("providers/acme/ranged", "1.5.0") // never read: a range
		e.tick()
		e.tick() // a replay must not open a second PR

		prs := e.gh.PRs("acme/app")
		if len(prs) != 2 {
			t.Fatalf("one PR per package, got %d", len(prs))
		}
		for _, pr := range prs {
			got, _ := e.gh.FileAt("acme/app", pr.Head, "infra/main.tf")
			var from, to string
			lock := false
			switch {
			case strings.Contains(pr.Head, "aws-4-2-0") || strings.Contains(pr.Head, "hashicorp-aws"):
				from, to, lock = `"~> 4.1.0"`, `"~> 4.2.0"`, true
			case strings.Contains(pr.Head, "vpc"):
				from, to = `version = "5.1.0"`, `version = "5.2.0"`
			default:
				t.Fatalf("unexpected branch %q", pr.Head)
			}
			if want := strings.Replace(tfE2E, from, to, 1); got != want {
				t.Errorf("%s: only that version moves, layout kept:\n%s", pr.Head, got)
			}
			if !strings.HasPrefix(pr.Head, "ripple/terraform/") {
				t.Errorf("branch %q", pr.Head)
			}
			if has := strings.Contains(pr.Body, "terraform init -upgrade"); has != lock {
				t.Errorf("%s: the provider-lock note belongs on provider PRs only (lock beside the file): %v\n%s", pr.Head, has, pr.Body)
			}
			if l, _ := e.gh.FileAt("acme/app", pr.Head, "infra/.terraform.lock.hcl"); l != "# lock\n" {
				t.Errorf("the lock file must be left alone, got %q", l)
			}
		}
		for _, r := range reg.Requests() {
			if r.Authorization != "" || !strings.HasPrefix(r.Path, "/v1/") || !strings.HasSuffix(r.Path, "/versions") {
				t.Errorf("request %+v: anonymous, and only for a package's versions", r)
			}
			if strings.Contains(r.Path, "ranged") || strings.Contains(r.Path, "example.com") {
				t.Errorf("a dependency ripple does not track was asked for: %s", r.Path)
			}
		}
		if len(e.gh.PRs("acme/optout")) != 0 || len(e.deps("acme/optout")) != 0 {
			t.Error("a repo that did not opt in was touched")
		}
	})
}

func TestTerraformDeniedPackageIsLeftAlone(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := terraformEnv(t, st)
		reg.Publish("providers/hashicorp/aws", "4.1.0")
		reg.Deny("providers/hashicorp/aws")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": terraformConfig, "main.tf": tfE2E})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Errorf("a package that refuses anonymous reads opened %d PRs", n)
		}
	})
}
