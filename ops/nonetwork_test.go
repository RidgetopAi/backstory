// TestNoNetworkInShippedCode keeps docs/WHAT-BACKSTORY-TOUCHES.md's "no
// network" claim true by gate: no non-test Go file under cmd/ or internal/
// may import an HTTP, RPC, SMTP or TLS package, or dial or listen on anything
// but a unix socket. Sync runs the user's own ssh as a subprocess instead.
package ops

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// noNetworkRoots are the trees scanned, relative to the repo root.
var noNetworkRoots = []string{"cmd", "internal"}

// forbiddenImports are import paths (and everything beneath them) that shipped
// code must not use.
var forbiddenImports = []string{"net/http", "net/rpc", "net/smtp", "crypto/tls"}

// allowedNetwork is the one network name shipped code may dial or listen on.
const allowedNetwork = "unix"

// netNetworkFuncs are package net functions whose first argument is the
// network name.
var netNetworkFuncs = map[string]bool{
	"Dial": true, "DialTimeout": true, "Listen": true, "ListenPacket": true,
}

// netAlwaysForbidden are package net identifiers that are network-specific by
// construction, or build a dialer or listener whose network is not checkable
// at the call site.
var netAlwaysForbidden = map[string]bool{
	"DialTCP": true, "DialUDP": true, "DialIP": true, "ListenTCP": true,
	"ListenUDP": true, "ListenIP": true, "ListenMulticastUDP": true,
	"Dialer": true, "ListenConfig": true,
}

// noNetworkExceptions names a file and the reason it may break the rule.
// Empty on purpose: an exception must be added here, with a reason, in review.
var noNetworkExceptions = map[string]string{}

// scanNoNetwork returns one violation line per offence in src.
func scanNoNetwork(filename string, src any) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	report := func(pos token.Pos, msg string) {
		out = append(out, fset.Position(pos).String()+": "+msg)
	}
	netNames := map[string]bool{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		for _, bad := range forbiddenImports {
			if p == bad || strings.HasPrefix(p, bad+"/") {
				report(imp.Pos(), "imports "+p)
			}
		}
		if p == "net" {
			name := "net"
			if imp.Name != nil {
				name = imp.Name.Name
			}
			netNames[name] = true
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || !netNames[id.Name] || !netNetworkFuncs[sel.Sel.Name] {
				return true
			}
			lit, ok := firstArgString(x)
			switch {
			case !ok:
				report(x.Pos(), "net."+sel.Sel.Name+" network argument is not a string literal")
			case lit != allowedNetwork:
				report(x.Pos(), "net."+sel.Sel.Name+" on network "+strconv.Quote(lit))
			}
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && netNames[id.Name] && netAlwaysForbidden[x.Sel.Name] {
				report(x.Pos(), "uses net."+x.Sel.Name)
			}
		}
		return true
	})
	return out, nil
}

func firstArgString(c *ast.CallExpr) (string, bool) {
	if len(c.Args) == 0 {
		return "", false
	}
	lit, ok := c.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

func TestNoNetworkInShippedCode(t *testing.T) {
	var violations []string
	scanned := 0
	for _, root := range noNetworkRoots {
		base := filepath.Join("..", root)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			scanned++
			rel := filepath.ToSlash(strings.TrimPrefix(path, ".."+string(filepath.Separator)))
			if _, ok := noNetworkExceptions[rel]; ok {
				return nil
			}
			v, perr := scanNoNetwork(path, nil)
			if perr != nil {
				return perr
			}
			violations = append(violations, v...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 50 {
		t.Fatalf("scanned only %d files; the walk is not finding the tree", scanned)
	}
	for rel, reason := range noNetworkExceptions {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("exception %s has no reason", rel)
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Errorf("network use in shipped code (docs/WHAT-BACKSTORY-TOUCHES.md promises none): %s", v)
	}
}

func TestNoNetworkScannerCatchesViolations(t *testing.T) {
	cases := []struct {
		name, src string
		want      int
	}{
		{"http import", "package p\nimport _ \"net/http\"\n", 1},
		{"http subpackage", "package p\nimport _ \"net/http/httputil\"\n", 1},
		{"rpc", "package p\nimport _ \"net/rpc\"\n", 1},
		{"smtp", "package p\nimport _ \"net/smtp\"\n", 1},
		{"tls", "package p\nimport _ \"crypto/tls\"\n", 1},
		{"tcp dial", "package p\nimport \"net\"\nfunc f() { net.Dial(\"tcp\", \"x:1\") }\n", 1},
		{"aliased listen", "package p\nimport n \"net\"\nfunc f() { n.Listen(\"tcp\", \":1\") }\n", 1},
		{"timeout udp", "package p\nimport \"net\"\nfunc f() { net.DialTimeout(\"udp\", \"x\", 0) }\n", 1},
		{"variable network", "package p\nimport \"net\"\nfunc f(s string) { net.Dial(s, \"x\") }\n", 1},
		{"dialer", "package p\nimport \"net\"\nvar _ net.Dialer\n", 1},
		{"unix dial ok", "package p\nimport \"net\"\nfunc f() { net.Dial(\"unix\", \"x\") }\n", 0},
		{"unix listen ok", "package p\nimport \"net\"\nfunc f() { net.Listen(\"unix\", \"x\") }\n", 0},
		{"net/url ok", "package p\nimport _ \"net/url\"\n", 0},
	}
	for _, c := range cases {
		got, err := scanNoNetwork("x.go", c.src)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(got) != c.want {
			t.Errorf("%s: got %d violations %v, want %d", c.name, len(got), got, c.want)
		}
	}
}
