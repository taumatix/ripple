# Terraform manager (Terraform registry)

Opt in per repo in `.ripple.yaml`:

```yaml
updates:
  - ecosystem: terraform
    directory: /          # optional; default /
```

Only `directory` applies (and the shared keys such as cooldown and grouping). Without the entry ripple does not read
`*.tf` files.

## What is read

In every `*.tf` file of the directory:

- `module "name" { source = "namespace/name/provider"  version = "1.2.3" }`, package `modules/namespace/name/provider`;
- `terraform { required_providers { aws = { source = "hashicorp/aws"  version = "~> 5.1.0" } } }`, package
  `providers/hashicorp/aws`.

A declaration is tracked when the source is a plain registry address (no host, not a `git::`, `./` or `//subdir`
address, only letters, digits, `_` and `-`) and the version is literal, with either

- three numeric segments and no operator or one of `=` and `~>` (`1.2.3`, `= 1.2.3`, `~> 1.2.3`), or
- two numeric segments after `~>` (`~> 1.2`).

The operator and the number of segments are kept when the version moves. The vendored version-constraints page says `~>`
"allows only the right-most version component to increment": `~> 1.1` allows 1.2 and 1.10 but not 2.0, `~> 1.0.4` allows
1.0.5 and 1.0.10 but not 1.1.0. Moving `~> 1.2` for release 1.5.3 writes `~> 1.5`: the same shape, and it allows 1.5.3. A
release whose X.Y is the one declared (1.2.9 for `~> 1.2`) is not proposed, because the text would not change.

Skipped: a floor, range or other operator (`>= 1.0`, `> 1.0`, `< 2.0`, `!= 1.0.0`, `>= 1.0.0, < 2.0.0`: the page's best
practices have a reusable module constrain "only [its] minimum allowed versions", so a floor says what the module needs,
not which release to use, and raising it would change what it supports; `TestRealtoolTerraformConstraintMeaning`
checks in CI that real terraform locks the newest release under a floor whatever its value, so a floor edit installs
nothing new, and that only a ceiling holds an install back, so widening one is a policy change, not an update; ROADMAP
250), a two-segment version after no
operator or `=`, an interpolated value, a module with no
`version`, a provider with no `source` (the legacy `aws = "~> 4.0"` form), a source with a registry host, attributes
declared twice, and anything in a `#`, `//` or `/* */` comment or in a heredoc.

## What is proposed

The versions of each tracked package are read from `https://registry.terraform.io/v1/modules/{ns}/{name}/{provider}/versions`
or `/v1/providers/{ns}/{type}/versions`, once per package per poll, anonymously. Only releases of three numeric segments
are proposed (no prerelease). The `cooldown` runs from the first poll that saw the version.

Ripple edits only the version digits. If the file changed since the poll the job is stale and nothing is written. A
change is applied to every declaration of that package at that version in the file.

## Lock file

`.terraform.lock.hcl` pins providers (never modules). Ripple does not run Terraform and leaves the lock alone: when a
provider PR edits a directory that has one, the PR body says it is stale and to run `terraform init -upgrade` and push to
the branch.

## Private registries

Hosted registries (`app.terraform.io/...`, `example.com/ns/name/provider`) are not read. A private module under a
plain `ns/name/provider` address answers 404 or 401 on the public registry and is left alone; one whose address also
exists publicly would be looked up there (dependency confusion), so do not enable this ecosystem for repos that rely on a
mirror that the public registry could shadow. The branch prefix is `ripple/terraform/`.

See [UPSTREAM.md](../UPSTREAM.md). Verified: the datasource against the real registry (`-tags live`) and the edit by
`terraform` (`TestRealtoolTerraform`, in CI; it moves a three-segment `~>`, a module and a two-segment `~> 3.1`). The
constraint semantics are the text of `docs/vendor/terraform/version-constraints.txt` (fetched 2026-11-25). The check
that `terraform init` records `constraints = "~> 3.2"` in the lock file ran for real and passed on the realtool
workflow (run 46108545867, 2026-11-28, the `terraform` job: `TestRealtoolTerraform` PASS, its `after` hook asserts it).
