package dynamofake

// ReservedWords lists the words the fake rejects unaliased, for the test that compares them with the vendored documentation.
func ReservedWords() []string {
	out := make([]string, 0, len(reserved))
	for w := range reserved {
		out = append(out, w)
	}
	return out
}
