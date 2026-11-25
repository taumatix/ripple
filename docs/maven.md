# Maven manager (Maven Central)

Opt in per repo in `.ripple.yaml`:

```yaml
updates:
  - ecosystem: maven
    directory: /          # optional; default /
```

Only `directory` applies (and the shared keys such as cooldown and grouping). Without the entry ripple does not read
`pom.xml` files.

## What is read

Every `<dependency>` of a `pom.xml`, in `<dependencies>`, in `<dependencyManagement>` and inside a `<plugin>`. The
package is `groupId:artifactId`. It is tracked when all of these hold:

- the `<version>` is literal text with three numeric segments, `1.2.3` (surrounding whitespace is kept);
- `groupId` and `artifactId` are literal and plain (letters, digits, `_`, `.`, `-`);
- the `pom.xml` declares no `<repositories>` and no `<pluginRepositories>`. Any entry and the whole file is left alone,
  because the artifact may come from that repository and ripple reads Maven Central only.

Skipped: a property (`${junit.version}`; ripple does not resolve properties, so a version bump through a property is
not proposed), a range (`[1.0,2.0)`), a qualified version (`1.0.0-SNAPSHOT`, `1.0.0.RELEASE`, `1.0-beta`), one or two or
four numeric segments (`4.13`), a dependency without `<version>` (managed by a parent or a BOM), `<exclusion>`
elements, the `<parent>` version, plugin versions themselves, and anything in an XML comment or CDATA.

## What is proposed

The versions of each tracked artifact are read from `https://repo.maven.apache.org/maven2/<group with / for .>/<artifact>/maven-metadata.xml`
(`<versioning><versions>`), once per artifact per poll, anonymously. Only releases of three numeric segments are
proposed. The `cooldown` runs from the first poll that saw the version.

Ripple edits only the version digits. If the file changed since the poll the job is stale and nothing is written. A
change is applied to every declaration of that artifact at that version in the file.

## Mirrors and settings.xml

`~/.m2/settings.xml` mirrors and profiles are not read: ripple sees only the repository's files. An artifact published
only to a private repository answers 404 on Maven Central and is left alone; one whose `groupId:artifactId` also exists on
Maven Central would be looked up there (dependency confusion), so do not enable this ecosystem for repos that
resolve internal coordinates through a mirror that Central could shadow.

No lock file is involved (Maven has none); the branch prefix is `ripple/maven/`.

See [UPSTREAM.md](../UPSTREAM.md): nothing here is verified against the real Maven Central beyond one sample
`maven-metadata.xml` (`docs/vendor/maven/junit-maven-metadata.xml`, fetched 2026-11-25).
