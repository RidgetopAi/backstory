package project

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseProjectRootsExpandsTildeAndCleansTrailingSlash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(projectRootsEnvVar, "~/Code/"+string(filepath.ListSeparator)+"~"+string(filepath.ListSeparator)+"relative"+string(filepath.ListSeparator)+"/abs/dir/")
	got := DefaultProjectRoots()
	want := []string{filepath.Join(home, "Code"), home, "/abs/dir"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultProjectRoots = %v, want %v", got, want)
	}
	t.Setenv(projectRootsEnvVar, "")
	if got := DefaultProjectRoots(); got != nil {
		t.Fatalf("unset roots = %v, want nil", got)
	}
}

func TestRootsScopeByPathComponentAndWorkspaceDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(projectRootsEnvVar, "~/R/")
	roots := DefaultProjectRoots()

	r := filepath.Join(home, "R")
	r2 := filepath.Join(home, "R2")
	cases := []struct {
		name string
		key  string
		top  string
		want bool
	}{
		{"root itself", "k", r, true},
		{"repo under root", "k", filepath.Join(r, "repo"), true},
		{"sibling sharing the name prefix", "k", filepath.Join(r2, "repo"), false},
		{"sibling itself", "k", r2, false},
		{"outside", "k", filepath.Join(home, "elsewhere"), false},
		{"workspace under root", workspaceKeyPrefix + filepath.Join(r, "ws"), "", true},
		{"workspace is the root", workspaceKeyPrefix + r, "", true},
		{"workspace in the prefix sibling", workspaceKeyPrefix + filepath.Join(r2, "ws"), "", false},
		{"bare-cwd key under root", filepath.Join(r, "scratch"), "", true},
		{"bare-cwd key outside", filepath.Join(r2, "scratch"), "", false},
	}
	for _, c := range cases {
		if got := KeyInRoots(roots, c.key, c.top); got != c.want {
			t.Errorf("%s: KeyInRoots = %v, want %v", c.name, got, c.want)
		}
	}
	if !KeyInRoots(nil, "k", "/anywhere") {
		t.Error("no roots must put everything in scope")
	}
}
