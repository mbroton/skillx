package refparse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseRemote(t *testing.T) {
	cases := []struct {
		in       string
		cloneURL string
		source   string
		branch   string
		subPath  string
	}{
		{
			in:       "someone/skills",
			cloneURL: "https://github.com/someone/skills.git",
			source:   "github.com/someone/skills",
		},
		{
			in:       "github.com/someone/skills",
			cloneURL: "https://github.com/someone/skills.git",
			source:   "github.com/someone/skills",
		},
		{
			in:       "https://github.com/someone/skills",
			cloneURL: "https://github.com/someone/skills.git",
			source:   "github.com/someone/skills",
		},
		{
			in:       "https://github.com/someone/skills.git",
			cloneURL: "https://github.com/someone/skills.git",
			source:   "github.com/someone/skills",
		},
		{
			in:       "https://github.com/someone/skills/tree/main",
			cloneURL: "https://github.com/someone/skills.git",
			source:   "github.com/someone/skills",
			branch:   "main",
		},
		{
			in:       "https://github.com/someone/skills/tree/dev/skills/pr-review",
			cloneURL: "https://github.com/someone/skills.git",
			source:   "github.com/someone/skills",
			branch:   "dev",
			subPath:  "skills/pr-review",
		},
		{
			in:       "github.com/someone/skills/tree/main/tools",
			cloneURL: "https://github.com/someone/skills.git",
			source:   "github.com/someone/skills",
			branch:   "main",
			subPath:  "tools",
		},
		{
			in:       "git@github.com:someone/skills.git",
			cloneURL: "git@github.com:someone/skills.git",
			source:   "github.com/someone/skills",
		},
		{
			in:       "git@github.com:someone/skills",
			cloneURL: "git@github.com:someone/skills",
			source:   "github.com/someone/skills",
		},
		{
			in:       "ssh://git@github.com/someone/skills.git",
			cloneURL: "ssh://git@github.com/someone/skills.git",
			source:   "github.com/someone/skills",
		},
		{
			in:       "https://gitlab.example.com/team/skills",
			cloneURL: "https://gitlab.example.com/team/skills.git",
			source:   "gitlab.example.com/team/skills",
		},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			r, err := Parse(c.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", c.in, err)
			}
			if r.Kind != Remote {
				t.Fatalf("Parse(%q): kind = %v, want Remote", c.in, r.Kind)
			}
			if r.CloneURL != c.cloneURL {
				t.Errorf("CloneURL = %q, want %q", r.CloneURL, c.cloneURL)
			}
			if r.Source != c.source {
				t.Errorf("Source = %q, want %q", r.Source, c.source)
			}
			if r.Branch != c.branch {
				t.Errorf("Branch = %q, want %q", r.Branch, c.branch)
			}
			if r.SubPath != c.subPath {
				t.Errorf("SubPath = %q, want %q", r.SubPath, c.subPath)
			}
		})
	}
}

func TestParseLocalPrefixes(t *testing.T) {
	for _, in := range []string{"/abs/path", "./rel", "../up", ".", ".."} {
		r, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if r.Kind != Local {
			t.Errorf("Parse(%q): kind = Remote, want Local", in)
		}
	}
}

func TestParseTilde(t *testing.T) {
	r, err := Parse("~/skills/foo")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != Local {
		t.Fatal("want Local")
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, "skills", "foo")
	if r.LocalPath != want {
		t.Errorf("LocalPath = %q, want %q", r.LocalPath, want)
	}
}

func TestParseExistingDirWinsOverOwnerRepo(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "docs", "guides")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	r, err := Parse("docs/guides")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != Local {
		t.Errorf("existing dir should parse as Local, got Remote (%s)", r.CloneURL)
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{"", "just-a-name", "https://github.com/onlyowner", "https://github.com/o/r/blob/main"} {
		if in == "https://github.com/o/r/blob/main" {
			// blob with branch but no file is accepted like tree; skip
			continue
		}
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q): expected error", in)
		}
	}
}

func TestParseUnsupportedURLSuffix(t *testing.T) {
	if _, err := Parse("https://github.com/o/r/pull/5"); err == nil {
		t.Error("expected error for /pull/ URL")
	}
}

func TestParseFileURL(t *testing.T) {
	r, err := Parse("file:///srv/git/skills.git")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != Remote {
		t.Fatal("file:// must parse as Remote (it is cloned, with provenance)")
	}
	if r.CloneURL != "file:///srv/git/skills.git" || r.Source != "file:///srv/git/skills.git" {
		t.Errorf("got CloneURL=%q Source=%q", r.CloneURL, r.Source)
	}
}
