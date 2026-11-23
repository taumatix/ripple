package engine

import (
	"strings"
	"testing"
)

func TestMinimalTidyKeepsWhatTidyWouldDrop(t *testing.T) {
	orig := "module m\n\ngo 1.21\n\nrequire (\n\texample.com/a v1.1.0\n\texample.com/unused v1.0.0 // indirect\n)\n"
	tidy := "module m\n\ngo 1.22\n\nrequire (\n\texample.com/a v1.1.0\n\texample.com/new v1.0.0 // indirect\n)\n"
	origSum := "example.com/a v1.0.0 h1:x=\nexample.com/a v1.0.0/go.mod h1:x=\nexample.com/unused v1.0.0 h1:u=\nexample.com/unused v1.0.0/go.mod h1:u=\n"
	tidySum := "example.com/a v1.1.0 h1:y=\nexample.com/a v1.1.0/go.mod h1:y=\nexample.com/new v1.0.0/go.mod h1:n=\n"
	mod, sum, err := minimalTidy("go.mod", []byte(orig), []byte(origSum), []byte(tidy), []byte(tidySum))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"go 1.22", "example.com/a v1.1.0", "example.com/unused v1.0.0 // indirect", "example.com/new v1.0.0 // indirect"} {
		if !strings.Contains(string(mod), want) {
			t.Errorf("go.mod lacks %q:\n%s", want, mod)
		}
	}
	wantSum := "example.com/a v1.1.0 h1:y=\nexample.com/a v1.1.0/go.mod h1:y=\nexample.com/new v1.0.0/go.mod h1:n=\nexample.com/unused v1.0.0 h1:u=\nexample.com/unused v1.0.0/go.mod h1:u=\n"
	if string(sum) != wantSum {
		t.Errorf("go.sum:\n%s\nwant\n%s", sum, wantSum)
	}
}

func TestMinimalTidyNoSum(t *testing.T) {
	m := "module m\n\ngo 1.22\n"
	mod, sum, err := minimalTidy("go.mod", []byte(m), nil, []byte(m), nil)
	if err != nil || sum != nil || string(mod) != m {
		t.Errorf("mod=%q sum=%v err=%v", mod, sum, err)
	}
}
