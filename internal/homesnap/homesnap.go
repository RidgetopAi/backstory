// Package homesnap records what an installer run did to a home directory —
// every path it created, changed or deleted — and checks those paths against
// an inventory of the files the installer is allowed to touch. The installer
// tests (ops/, internal/install) observe the real runs with it rather than
// reading the scripts, so the inventory is checked against what the code
// actually does.
package homesnap

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Snapshot maps each path under a root (slash-separated, relative) to what is
// there: a directory, a symlink and its target, or a file's mode and sha256.
type Snapshot map[string]string

// Take walks root without following symlinks.
func Take(root string) (Snapshot, error) {
	s := Snapshot{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == "." {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			s[filepath.ToSlash(rel)] = "symlink " + target
		case d.IsDir():
			s[filepath.ToSlash(rel)] = "dir"
		default:
			data, err := os.ReadFile(p) //nolint:gosec // a path under the caller's test root
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			s[filepath.ToSlash(rel)] = info.Mode().String() + " " + hex.EncodeToString(sum[:])
		}
		return nil
	})
	return s, err
}

// Changed is every path created, changed or deleted between before and
// after, sorted.
func Changed(before, after Snapshot) []string {
	var out []string
	for p, v := range after {
		if before[p] != v {
			out = append(out, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// Row is one inventory entry: Pattern names the paths (relative to home,
// path.Match syntax per path, or a "dir/**" prefix for a tree); the other
// fields say who writes them and what happens to an edited, symlinked or
// shipped copy on install and on remove.
type Row struct {
	Pattern   string
	Writer    string
	OnInstall string
	OnRemove  string
}

// Match reports whether rel is one of pattern's paths.
func Match(pattern, rel string) bool {
	if dir, ok := strings.CutSuffix(pattern, "/**"); ok {
		return rel == dir || strings.HasPrefix(rel, dir+"/")
	}
	ok, err := path.Match(pattern, rel)
	return err == nil && ok
}

// Unlisted is every path in changed that no row matches.
func Unlisted(changed []string, rows []Row) []string {
	var out []string
	for _, p := range changed {
		if !slices.ContainsFunc(rows, func(r Row) bool { return Match(r.Pattern, p) }) {
			out = append(out, p)
		}
	}
	return out
}

// Unseen is every row that matches none of seen: an inventory entry no
// observed run touched.
func Unseen(seen []string, rows []Row) []Row {
	var out []Row
	for _, r := range rows {
		if !slices.ContainsFunc(seen, func(p string) bool { return Match(r.Pattern, p) }) {
			out = append(out, r)
		}
	}
	return out
}
