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

Also tracked: the two-segment `^1.2`, `~1.2` and `^0.3`. Composer's versions article gives `~1.2` as `>=1.2 <2.0.0`
and `^0.3` as `>=0.3.0 <0.4.0`, so a later release of the same line is already allowed and needs a lock refresh, not an
edit. Only a release outside the line is proposed: a later major (`^1.2`, `~1.2`) or, for `^0.Y`, a later minor. The text
becomes that release's first two segments (`^1.2` to `^2.4` for 2.4.1; `^0.3` to `^0.4` for 0.4.2), which it satisfies,
and a later major is a `major` update under the usual policy.

Also tracked, after real Composer 2 was asked what each locks (`TestRealtoolComposerConstraintMeaning`, run in CI
`realtool / composer` against Packagist, 2026-11-27): `~0.3` (locks mockery 0.9.11, so `>=0.3 <1.0`: only a later major
moves it, to the release's first two segments, `~0.3` to `~1.2` for 1.2.0), and the one-segment `^1`, `~1` and `^0`
(`^1` and `~1` lock monolog 1.27.1, `^0` locks mockery 0.9.11, so each is `>=X <X+1`: only a later major moves it, to
that release's major alone, `^1` to `^2` for 2.4.1). `^0.3` is unchanged: `>=0.3.0 <0.4.0`, so a 0.4 release moves it.
Nothing was run for `=1` or a bare `1`, so they stay skipped.

Skipped: a range (`>=1.0 <2.0`), several constraints (`^1.0 || ^2.0`), a wildcard (`1.*`), a bare `1` or `=1`,
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
