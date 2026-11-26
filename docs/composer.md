# Composer manager (Packagist)

Opt in per repo in `.ripple.yaml`:

```yaml
updates:
  - ecosystem: composer
    directory: /          # optional; default /
```

Only `directory` applies (and the shared keys such as cooldown and grouping). Without the entry ripple does not read
`composer.json` files.

## What is read

The `require` and `require-dev` objects of a `composer.json`. A package is tracked when all of these hold:

- the name is `vendor/project` (platform requirements such as `php`, `ext-json` and `lib-*` have no slash and are skipped);
- the version is one release in the forms `1.2.3`, `=1.2.3`, `^1.2.3` or `~1.2.3` (three numeric segments, an optional
  `v` before them, kept as written);
- the version string has no JSON escape in it (it is edited byte for byte);
- the file has no `repositories` entry. Any entry (a private Composer repository, a VCS or path source, or
  `{"packagist.org": false}`) and the whole file is left alone, because the package may not come from Packagist.

Skipped: a range (`>=1.0 <2.0`), several constraints (`^1.0 || ^2.0`), a wildcard (`1.*`), a two-segment `^1.2`
(the constraint already allows every later 1.x, so a new release needs a lock refresh and no `composer.json` edit),
a stability flag, a `dev-` branch, four-segment or prerelease versions. Other keys (`extra`, `suggest`, `conflict`,
`replace`, `provide`) are not read.

## What is proposed

The versions of each tracked package are read from `https://repo.packagist.org/p2/<vendor>/<package>.json` (the
Composer v2 metadata: tagged releases only), once per package per poll, anonymously. Prereleases, branches and versions
that are not three numeric segments are never proposed. The `cooldown` runs from the first poll that saw the version
(`published-time` is not read).

Ripple edits only the version digits (the `^`, `~`, `=` or `v` prefix, quotes and layout stay). If the file changed since
the poll the job is stale and nothing is written.

## Lock files

`composer.lock` beside the edited file is not rewritten. The PR body names it as stale; run `composer update
<package>` and push to the branch.

The branch prefix is `ripple/packagist/`.

See [UPSTREAM.md](../UPSTREAM.md). The version listing is read live from the real Packagist by a `-tags live` test
([live-tests.md](live-tests.md)) and an edited `composer.json` is accepted by Composer in the real-tool test
([realtool.md](realtool.md)); rate limits and redirects are not checked.
