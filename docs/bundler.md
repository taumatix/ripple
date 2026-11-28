# Bundler manager (RubyGems)

Opt in per repo in `.ripple.yaml`:

```yaml
updates:
  - ecosystem: bundler
    directory: /          # optional; default /
```

Only `directory` applies (and the shared keys such as cooldown and grouping). Without the entry ripple does not read
`Gemfile` files.

## What is read

`gem "name", "<version>"` lines of a `Gemfile`. A gem is tracked when all of these hold:

- the version is one release in the forms `1.2.3`, `= 1.2.3` or `~> 1.2.3` (three numeric segments), or the
  two-segment pessimistic `~> 1.2` or the one-segment `~> 1`;
- the line is complete on one line (a trailing comma continues on the next, and is skipped);
- the gem has no `git:`, `github:`, `path:`, `source:` (or `:git =>` style) option and is not inside a `git`, `path`,
  `github` or `source` block;
- the file names no `source` other than `https://rubygems.org`. One other source and the whole file is left alone.

Skipped: a gem with no version, several constraints (`">= 1", "< 2"`), a range, a single `">= 1.0"` or `"> 1.0"`
(it already allows every later release, so there is never anything to write), `<`, `<=` and
`!=` constraints (no new text follows from a release alone), four-segment or prerelease versions, `.gemspec` files and
`gems.rb`.

### The two-segment `~> 1.2`

The Gemfile man page ([docs/vendor/rubygems/gemfile.md](vendor/rubygems/gemfile.md), "Gems > Version") defines `~> 2.1`
as `>= 2.1` and `< 3.0`, and `~> 2.0.3` as `>= 2.0.3` and `< 2.1`. So `~> 1.2` already allows every later 1.x: a release
of the same major is never proposed (a `Gemfile.lock` refresh picks it up). A release of a later major is proposed as a
`major` update (the same `update-types` rules, cooldown and no auto-merge default as every other manager), and the file
then takes that release's first two segments: `~> 1.2` becomes `~> 2.1` for 2.1.3. The new constraint always allows the
release it names and never a lower major, so it is never wider than what that release implies.

### The one-segment `~> 1`

The man page does not define `~> 1`. The real tool does: `bundle lock` on a Gemfile with `gem "rack", "~> 1"` locks
rack 1.6.13, with `~> 2` rack 2.2.26 and with `~> 0` rack 0.9.1 (checked 2026-11-27 against rubygems.org with Bundler's
own resolver, and asserted on every run by `TestRealtoolBundlerOneSegment`). So `~> 1` is `>= 1, < 2`, the same as
`~> 1.0`, and is treated like the two-segment form: a release of the same major is never proposed, a release of a later
major is, and the file takes that release's major alone (`~> 1` becomes `~> 2` for 2.2.26).

### The three-segment `~> 2.2.4`

Asked of real `bundle lock` too (`TestRealtoolBundlerThreeSegment`): `gem "rack", "~> 2.2.4"` locks a 2.2 rack, so the
constraint pins the minor and every release of another minor is an update. The file takes the whole new version
(`~> 3.1.0` for 3.1.0), and `bundle lock` then locks a 3.1 rack.

## What is proposed

The versions of each tracked gem are read from `https://rubygems.org/api/v1/versions/<gem>.json`, once per gem per poll.
Prereleases, builds for a platform other than `ruby`, and versions that are not three numeric segments are never
proposed. The `cooldown` runs from the first poll that saw the version (`created_at` is not read).

Ripple edits only the version digits (the `= ` or `~> ` prefix, quotes, comments and options stay), so the file keeps
its layout. If the file changed since the poll the job is stale and nothing is written.

## Lock files

`Gemfile.lock` beside the edited Gemfile is not rewritten. The PR body names it as stale; run `bundle install` and push
to the branch.

The branch prefix is `ripple/rubygems/`.

See [UPSTREAM.md](../UPSTREAM.md). The version listing is read live from the real rubygems.org by a `-tags live` test
([live-tests.md](live-tests.md)) and an edited Gemfile is accepted by `bundle lock` in the real-tool test
([realtool.md](realtool.md)); rate limits and yanked versions are not checked.
