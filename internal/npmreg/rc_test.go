package npmreg

import "testing"

func TestIsPublic(t *testing.T) {
	for in, want := range map[string]bool{
		"https://registry.npmjs.org/": true, "https://registry.npmjs.org": true, "https://Registry.NPMJS.org/x": true,
		"https://registry.yarnpkg.com":               true,
		"http://registry.npmjs.org/":                 false, // not https
		"https://mirror.example/registry.npmjs.org/": false,
		"https://registry.npmjs.org.evil.example/":   false,
		"https://registry.npmjs.org@evil.example/":   false,
		"https://user:pw@registry.npmjs.org/":        false,
		"${NPM_REGISTRY}":                            false,
		"https://${HOST}/":                           false, "": false, "registry.npmjs.org": false,
	} {
		if got := IsPublic(in); got != want {
			t.Errorf("IsPublic(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseNpmrc(t *testing.T) {
	rc := ParseNpmrc([]byte("# c\n; c\n\nregistry = 'https://r.example/'\n@Corp:registry=https://c.example/\n//c.example/:_authToken=SECRET\n@empty:registry=\nfoo\n"), ".npmrc")
	if rc.Registry != (Setting{"https://r.example/", ".npmrc"}) {
		t.Errorf("registry = %+v", rc.Registry)
	}
	if s := rc.For("@corp/pkg"); s.URL != "https://c.example/" {
		t.Errorf("scope lookup is case-insensitive: %+v", s)
	}
	if s := rc.For("@empty/pkg"); s != rc.Registry {
		t.Errorf("an empty scope value is no setting: %+v", s)
	}
	if len(rc.Scopes) != 1 {
		t.Errorf("only registry keys are kept, never credentials: %+v", rc)
	}
}

func TestParseYarnrcs(t *testing.T) {
	rc := ParseYarnrc([]byte("# c\nregistry \"https://r.example/\"\n\"@corp:registry\" \"https://c.example/\"\n"), ".yarnrc")
	if rc.Registry.URL != "https://r.example/" || rc.For("@corp/x").URL != "https://c.example/" {
		t.Errorf("%+v", rc)
	}
	rc = ParseYarnrcYml([]byte("npmRegistryServer: \"https://r.example\"\nnpmScopes:\n  corp:\n    npmRegistryServer: https://c.example\n  '@other':\n    npmRegistryServer: https://o.example\n"), ".yarnrc.yml")
	if rc.Registry.URL != "https://r.example" || rc.For("@corp/x").URL != "https://c.example" || rc.For("@other/x").URL != "https://o.example" {
		t.Errorf("%+v", rc)
	}
	if rc := ParseYarnrcYml([]byte(":\n\t- ["), "f"); rc.Registry.URL != "" {
		t.Errorf("not YAML: %+v", rc)
	}
}

func TestLayering(t *testing.T) {
	near := ParseNpmrc([]byte("@a:registry=https://registry.npmjs.org/\n"), "web/.npmrc")
	far := ParseNpmrc([]byte("@a:registry=https://a.example/\n@b:registry=https://b.example/\nregistry=https://d.example/\n"), ".npmrc")
	rc := near.Over(far)
	if rc.For("@a/x").URL != "https://registry.npmjs.org/" || rc.For("@b/x").URL != "https://b.example/" || rc.For("plain").URL != "https://d.example/" {
		t.Errorf("nearest wins per key: %+v", rc)
	}
	// Files of one level that disagree never read as public.
	j := ParseNpmrc([]byte("registry=https://registry.npmjs.org/\n"), "a").Join(ParseNpmrc([]byte("registry=https://p.example/\n"), "b"))
	if j.Registry.URL != "https://p.example/" {
		t.Errorf("join: %+v", j.Registry)
	}
	j = ParseNpmrc([]byte("registry=https://p.example/\n"), "a").Join(ParseNpmrc([]byte("registry=https://registry.npmjs.org/\n"), "b"))
	if j.Registry.URL != "https://p.example/" {
		t.Errorf("join, other order: %+v", j.Registry)
	}
}
