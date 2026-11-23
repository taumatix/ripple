package github

import "testing"

func TestGraphQLURLDerivation(t *testing.T) {
	for base, want := range map[string]string{
		"https://api.github.com":               "https://api.github.com/graphql",
		"https://api.github.com/":              "https://api.github.com/graphql",
		"https://ghes.example.com/api/v3":      "https://ghes.example.com/api/graphql",
		"https://ghes.example.com/api/v3/":     "https://ghes.example.com/api/graphql",
		"https://ghes.example.com:8443/api/v3": "https://ghes.example.com:8443/api/graphql",
		"https://corp.example.com/gh/api/v3":   "https://corp.example.com/gh/api/graphql",
		"http://127.0.0.1:9/":                  "http://127.0.0.1:9/graphql",
	} {
		if got := GraphQLURL(base); got != want {
			t.Errorf("GraphQLURL(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestClientGraphQLOverrideWins(t *testing.T) {
	c := &Client{APIBase: "https://h/api/v3", GraphQLURL: "https://gql.example/query"}
	if got := c.graphQLURL(); got != "https://gql.example/query" {
		t.Errorf("got %q", got)
	}
}

func TestVersionsFallBackOnceUnlessPinned(t *testing.T) {
	var nilV *Versions
	if nilV.Current() != APIVersion || nilV.Lower(APIVersion) {
		t.Error("nil Versions must send APIVersion and never lower")
	}
	v := &Versions{}
	if !v.Lower(APIVersion) || v.Current() != FallbackAPIVersion {
		t.Fatalf("first 410 must lower to %s, got %s", FallbackAPIVersion, v.Current())
	}
	if !v.Lower(APIVersion) { // a concurrent request that sent the old value retries too
		t.Error("a request sent with the old version should retry")
	}
	if v.Lower(FallbackAPIVersion) {
		t.Error("410 on the fallback itself is final")
	}
	p := PinnedVersions("2022-11-28")
	if p.Lower("2022-11-28") || p.Current() != "2022-11-28" {
		t.Error("pinned versions never fall back")
	}
}
