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

- the version is one release in the forms `1.2.3`, `= 1.2.3` or `~> 1.2.3` (three numeric segments);
- the line is complete on one line (a trailing comma continues on the next, and is skipped);
- the gem has no `git:`, `github:`, `path:`, `source:` (or `:git =>` style) option and is not inside a `git`, `path`,
  `github` or `source` block;
- the file names no `source` other than `https://rubygems.org`. One other source and the whole file is left alone.

Skipped: a gem with no version, several constraints (`">= 1", "< 2"`), a range, a two-segment `"~> 1.2"` (the Gemfile
already allows every later 1.x, so a new release needs a lock refresh and no Gemfile edit), four-segment or
prerelease versions, `.gemspec` files and `gems.rb`.

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
