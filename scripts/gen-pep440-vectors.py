#!/usr/bin/env python3
"""Regenerates internal/pypi/testdata/pep440.json from the `packaging` library.

    pip install packaging
    python3 scripts/gen-pep440-vectors.py > internal/pypi/testdata/pep440.json

The Go tests read the committed table, so CI needs no Python. The table records the
packaging version it came from; rerun the script to refresh it and review the diff.
"""
import json, random, sys
import packaging
from packaging.specifiers import SpecifierSet
from packaging.version import InvalidVersion, Version

fixed = """1.0.dev1 1.0a1 1.0a2.dev3 1.0a2 1.0b1 1.0rc1 1.0 1.0.post1 1.0.post2 1.1.dev1 1.1 1.10 2!0.1 1.0.0 1.0.0.0
1.0RC1 1.0c1 v1.0-alpha.2 1.0a2 1.0-1 1.0.post1 1.0.rev3 1.0post3 1.0-r4 1.0.post 1.0.post0 1.0.dev 1.0.dev0 1.0.a 1.0a 1.0.a0
1.0+abc 1.0+ABC 1.0+1 1.0+1.2 1.0+abc.1 1.0+abc.2 1.0+5 1.0+abc-def 1.0+abc_def 1.0.post1+local 1.0a1+local 1.0.dev1+x
 1.0 1.0\t 0!1.0 00.1 01.002 1.0.0.0.0 1!1.0 1!0 3.14159 1.2.3.4.5.6 2020.1.1 1.0.post1.dev1 1.0.post1.dev0 1.0a1.post1
1.0a1.post1.dev1 1.0rc1.post1 1.0.dev1.post1 1.0-a1 1.0_a1 1.0.alpha1 1.0.beta2 1.0.preview3 1.0.pre4 1.0-pre-4 1.0RC.1 1.0.Rev.2
4.3.0.dev0 4.2.0rc1 4.2 4.2.1 4.1.13 0.0.0 0 1 2 10 9
1.0.0a1 1.0.0b1 1.0.0rc1 1.0.0rc2 1.0.0.post1 1.0.0.dev1 1.0.1.dev1 1.0.1a1 2.0.0.dev9 1.0.dev99 1.0.dev100""".replace("\\t", "\t").split("\n")
words = [w for line in fixed for w in line.split(" ")]
invalid = ["", "abc", "1.0.x", "1..0", "1.0-", "1.0+", "1.0+_", "1!", "!1", "1.0.0-beta-x", "1.0.1-dev-x", "v", "1.0++x", "1.0 1.0", "-1", "1.-1", "1.0a-1x", "1,0", "1.0.*"]

rng = random.Random(440)
epochs, rels = ["", "1!", "2!"], ["0", "1", "1.0", "1.0.0", "1.1", "1.10", "2", "2.0.1", "10", "0.9.9"]
pres = ["", "", "a1", "b2", "rc1", "c3", "alpha4", "beta", "-rc.7", "a0", "rc10"]
posts = ["", "", ".post1", "-1", ".rev2", "post", ".post10"]
devs = ["", "", ".dev0", ".dev5", "dev12"]
locs = ["", "", "", "+abc", "+1.2", "+abc.5", "+2"]
for _ in range(300):
    words.append(rng.choice(epochs) + rng.choice(rels) + rng.choice(pres) + rng.choice(posts) + rng.choice(devs) + rng.choice(locs))

seen, good = set(), []
for w in words + invalid:
    if w in seen:
        continue
    seen.add(w)
    try:
        good.append((w, Version(w)))
    except InvalidVersion:
        if w not in invalid:
            invalid.append(w)
good_set = {w for w, _ in good}
invalid = [w for w in dict.fromkeys(invalid) if w not in good_set]
uniq = sorted({v for _, v in good})
rank = {v: i for i, v in enumerate(uniq)}
versions = [{"in": w, "norm": str(v), "rank": rank[v]} for w, v in good]

specs = [">=1.0", ">1.0", "<=1.0", "<1.0", "==1.0", "!=1.0", "~=1.4", "~=1.4.2", "==1.*", "!=1.*", "==1.0.*", ">=1.0,<2", ">=1.10,<2.0,!=1.5.*",
         ">1.0.post1", "<1.0.post1", ">=1!0", "<=2.0.0", "~=2.0", "==1.0.0", "!=1.0.0", ">1", "<2", ">=0,<1.1", "~=0.9.9"]
finals = [(w, v) for w, v in good if not v.is_prerelease and not v.is_devrelease and v.local is None]
cases = []
for spec in specs:
    ss = SpecifierSet(spec)
    for w, v in finals:
        cases.append({"spec": spec, "version": w, "ok": ss.contains(v, prereleases=True)})

json.dump({"packaging": packaging.__version__, "versions": versions, "invalid": invalid, "specifiers": cases},
          sys.stdout, indent=0, separators=(",", ":"))
print()
