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

- the `<version>` is literal text with two or three numeric segments, `4.13` or `1.2.3` (surrounding whitespace is
  kept), or is `${name}` for a property that qualifies as below;
- `groupId` and `artifactId` are literal and plain (letters, digits, `_`, `.`, `-`);
- the `pom.xml` declares no `<repositories>` and no `<pluginRepositories>`. Any entry and the whole file is left alone,
  because the artifact may come from that repository and ripple reads Maven Central only.

Two-segment versions: `4.13` is a plain release. Maven trims trailing zeros before comparing, so `4.13` and `4.13.0`
are the same version (vendored `docs/vendor/maven/pom-reference.txt`, "Version Order Specification": `1.0 -> 1`,
`1.0.0 -> 1`). A two-segment declaration may move to a two- or a three-segment release (`4.13` to `4.13.2` or `4.14`);
a three-segment declaration still moves to three-segment releases only, as before.

Properties (`${x.version}`): the property's value is the text that moves, and the dependency keeps `${x.version}`.
This happens only when all of these hold; otherwise the dependency is left alone, unread (the engine logs each such
declaration with its reason, "declaration left unread"; there is no dashboard report yet, ROADMAP 320):
- the property is declared exactly once in the whole file, as a direct child of `<project><properties>` (a profile's
  `<properties>` is a different set, and a name declared there as well is ambiguous), and its whole text is a literal
  `X.Y` or `X.Y.Z`;
- every `${x.version}` in the file, outside comments (CDATA, other properties, plugin versions, descriptions
  included), is the `<version>` of a dependency. A property shared by several artifacts (`jackson-core` and
  `jackson-databind`) is one entry, tracked under the first artifact that uses it; when a release is proposed ripple
  reads the releases of every other user and moves the property only to a release all of them have (the newest common
  one, if the proposed one is missing for a sharer, else the job is skipped);
- the name is not one Maven resolves itself (`project.`, `pom.`, `env.`, `settings.` and the Java system properties,
  `pom-reference.txt` "Properties"), and the file is well-formed XML.
The property is the pom's own: one inherited from a `<parent>` is not followed, and an override on the command line
(`-Dx.version=...`) is not seen; real Maven lets it win over the pom (`TestRealtoolMavenSharedProperty`).

The `<parent>` version is tracked as a dependency (`groupId:artifactId` of the parent) and its `<version>` moves like
any literal. The parent's own properties and managed versions are not read, and bumping a parent changes every version it
manages (`TestRealtoolMavenParent`), which the PR does not list (ROADMAP 323).

Skipped: a range (`[1.0,2.0)`), a qualified version (`1.0.0-SNAPSHOT`, `1.0.0.RELEASE`, `1.0-beta`), one or four numeric
segments, a property that does not qualify, a dependency without `<version>` (managed by a parent or a BOM),
`<exclusion>` elements, plugin versions themselves, and anything in an XML comment or CDATA.

## What is proposed

The versions of each tracked artifact are read from `https://repo.maven.apache.org/maven2/<group with / for .>/<artifact>/maven-metadata.xml`
(`<versioning><versions>`), once per artifact per poll, anonymously. Only releases of two or three numeric segments are
proposed. The `cooldown` runs from the first poll that saw the version.

Ripple edits only the version digits. If the file changed since the poll the job is stale and nothing is written. A
change is applied to every declaration of that artifact at that version in the file.

## Mirrors and settings.xml

`~/.m2/settings.xml` mirrors and profiles are not read: ripple sees only the repository's files. An artifact published
only to a private repository answers 404 on Maven Central and is left alone; one whose `groupId:artifactId` also exists on
Maven Central would be looked up there (dependency confusion), so do not enable this ecosystem for repos that
resolve internal coordinates through a mirror that Central could shadow.

No lock file is involved (Maven has none); the branch prefix is `ripple/maven/`.

See [UPSTREAM.md](../UPSTREAM.md). Verified: the datasource against the real Maven Central (`-tags live`), and the
edit by `mvn` (`TestRealtoolMaven`, `TestRealtoolMavenSharedProperty` and `TestRealtoolMavenParent`, in CI: a literal, a two-segment version, a property, a shared property and a parent). The property rules
are from `docs/vendor/maven/pom-reference.txt` (fetched 2026-11-26); that Maven resolves a property declared once in
`<project><properties>` the way described, with a command-line override winning, is that page's text, not something
run here (assumed, unverified beyond the realtool pom).
