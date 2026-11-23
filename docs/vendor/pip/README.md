# PyPI, PEP 440 and requirements-file excerpts

Fetched 2026-11-23 (via a summarising fetch, so the wording is condensed, not copied byte for byte) and read before
`internal/pypi` and `internal/manager/pip.go` were written.

Sources: <https://docs.pypi.org/api/json/>, <https://peps.python.org/pep-0440/>,
<https://pip.pypa.io/en/stable/reference/requirements-file-format/>.

## PyPI JSON API

- `GET /pypi/<project>/json` returns the project's latest info and every release; `GET /pypi/<project>/<version>/json`
  one release.
- `releases` is an object keyed by version string; each value is an array of file objects. The documentation calls
  `releases` "considered deprecated" in favour of the Index API (PEP 503/691); ripple still reads it (see UPSTREAM.md).
- A file object has `filename`, `url`, `packagetype`, `python_version`, `size`, `upload_time`, `upload_time_iso_8601`,
  `digests`, `yanked` (boolean) and `yanked_reason` (string or null), among others. ripple reads `yanked` only.

## PEP 440

- Public version: `[N!]N(.N)*[{a|b|rc}N][.postN][.devN]` (+ an optional local segment `+abc.1`).
- Normalization: case-insensitive; `alpha`, `beta`, `c`, `pre`, `preview` mean `a`, `b`, `rc`; separators `-`, `_`, `.`
  before a pre/post/dev segment are optional; an omitted number is 0; a leading `v` is ignored; `rev`/`r` and a bare
  `-N` mean post.
- Ordering within one release: `.devN` < `aN` < `bN` < `rcN` < (none) < `.postN`; an epoch dominates, release segments
  compare numerically with missing trailing segments as 0.
- `~=1.4.5` means `>=1.4.5, ==1.4.*`; `==` accepts a trailing `.*`; `===` is a string comparison.

## Requirements files

- A trailing backslash continues the line; `#` at the start of a line, or after whitespace, starts a comment (applied
  after continuation).
- `--hash` is a per-requirement option; `-i/--index-url`, `--extra-index-url`, `-r`, `-c`, `-e`, `-f` are global options.

## Assumed, unverified (not in the excerpts above)

- pypi.org serves `/pypi/<name>/json` anonymously and answers 404 for an unknown project; whether a non-normalized name
  is redirected is not documented, so ripple always asks for the PEP 503 normalized name (lowercase, runs of `-_.` as `-`).
- A release whose files are all yanked is skipped; a release with no files is skipped (nothing installable).
- PEP 508 grammar is read by a scanner as far as docs/pip.md says, not parsed in full. TOML is read by a scanner, not parsed.
