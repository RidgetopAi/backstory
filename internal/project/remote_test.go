package project

import "testing"

func TestNormalizeRemote(t *testing.T) {
	const gh = "github.com/x/app"
	same := []string{
		"git@github.com:X/App.git",
		"git@github.com:x/app",
		"https://github.com/x/app",
		"https://github.com/X/App.git",
		"https://user:tok@GitHub.com/x/app/",
		"ssh://git@github.com/x/app",
		"ssh://git@github.com:22/x/app.git",
		"git://github.com/x/app.git",
		"  https://github.com/x/app.git  ",
	}
	for _, in := range same {
		if got, ok := NormalizeRemote(in); !ok || got != gh {
			t.Errorf("NormalizeRemote(%q) = %q, %v; want %q, true", in, got, ok, gh)
		}
	}

	if a, _ := NormalizeRemote("https://github.com/x/app"); true {
		if b, _ := NormalizeRemote("https://github.com/x/other"); a == b {
			t.Errorf("different repos share id %q", a)
		}
	}

	// Path case is kept on hosts that are not known case-insensitive forges.
	if got, ok := NormalizeRemote("git@git.example.com:Team/App.git"); !ok || got != "git.example.com/Team/App" {
		t.Errorf("self-hosted = %q, %v", got, ok)
	}
	if a, _ := NormalizeRemote("git@git.example.com:Team/App"); a == "git.example.com/team/app" {
		t.Errorf("self-hosted path case was folded")
	}

	for _, in := range []string{
		"", "   ", "/home/me/code/app", "./app", "../app", "app", "~/app",
		"file:///home/me/app.git", "file://localhost/x/app", `C:\code\app`, "C:/code/app",
		"https://github.com", "https://github.com/", "git@github.com:", "ftp://github.com/x/app",
		"://bad", "https://",
	} {
		if got, ok := NormalizeRemote(in); ok {
			t.Errorf("NormalizeRemote(%q) = %q, true; want false", in, got)
		}
	}
}
