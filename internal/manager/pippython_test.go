package manager

import "testing"

func TestPipPythonSpecFloor(t *testing.T) {
	for spec, want := range map[string]string{
		">=3.9":         "3.9",
		">=3.8,<4":      "3.8",
		">=3.8, >=3.10": "3.10",
		"~=3.9":         "3.9",
		"^3.9":          "3.9",
		"~3.9.1":        "3.9.1",
		"==3.11.*":      "3.11",
		"3.11":          "3.11",
		"^3.8 || ^3.10": "3.8",
		"<4":            "",
		">=3.8 || <3":   "",
		"*":             "",
		"!=3.0.*":       "",
		"bogus":         "",
	} {
		if got := PipPythonSpecFloor(spec); got != want {
			t.Errorf("%q: floor %q, want %q", spec, got, want)
		}
	}
}

func TestPipPythonFloorFiles(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"pyproject.toml", "[project]\nrequires-python = \">=3.10\"\n", "3.10"},
		{"pyproject.toml", "[tool.poetry.dependencies]\npython = \"^3.9\"\nrequests = \"^2\"\n", "3.9"},
		{"pyproject.toml", "[project]\nname = \"x\"\n", ""},
		{"pyproject.toml", "not = = toml", ""},
		{"Pipfile", "[requires]\npython_version = \"3.11\"\n", "3.11"},
		{"Pipfile", "[requires]\npython_full_version = \"3.11.4\"\n", "3.11.4"},
		{"sub/.python-version", "# pin\n3.12.1\n", "3.12.1"},
		{".python-version", "pypy3.10\n", ""},
	} {
		if got := PipPythonFloor(c.name, []byte(c.src)); got != c.want {
			t.Errorf("%s %q: %q, want %q", c.name, c.src, got, c.want)
		}
	}
}
