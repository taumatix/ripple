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
address, only letters, digits, `_` and `-`) and the version is literal, three numeric segments, with no operator or one of
`=` and `~>` (`1.2.3`, `= 1.2.3`, `~> 1.2.3`). The operator is kept when the version moves.

Skipped: a range or any other operator (`>= 1.0.0`, `~> 1.2`, `1.0.0, < 2.0.0`), an interpolated value, a module with no
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

See [UPSTREAM.md](../UPSTREAM.md): nothing here is verified against the real registry; the protocol pages
(`docs/vendor/terraform`) were fetched 2026-11-25 from developer.hashicorp.com and the fake is written from them.
