package panel

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// storeRecordKinds reads the kinds the store defines from SCHEMA.md's
// records.kind CHECK, so a kind added to the store fails this suite until
// the panel maps it (task 0667fadb).
func storeRecordKinds(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../SCHEMA.md")
	if err != nil {
		t.Fatalf("read SCHEMA.md: %v", err)
	}
	m := regexp.MustCompile(`kind\s+TEXT NOT NULL CHECK \(kind IN\s*\(([^)]*)\)`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("records.kind CHECK not found in SCHEMA.md")
	}
	var kinds []string
	for _, k := range strings.Split(string(m[1]), ",") {
		kinds = append(kinds, strings.Trim(strings.TrimSpace(k), "'"))
	}
	if len(kinds) < 8 {
		t.Fatalf("parsed only %v", kinds)
	}
	return kinds
}

func evalKindMaps(t *testing.T, expr string) map[string]string {
	t.Helper()
	raw := evalJS(t, expr, "js/glyphs.js", "js/memory.js")
	var out map[string]string
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

func TestRecordKindGlyphsAndLabels(t *testing.T) {
	kinds := storeRecordKinds(t)
	kj := jsonLiteral(t, kinds)
	glyphs := evalKindMaps(t, `(function(){var o={};`+kj+`.forEach(function(k){o[k]=recordKind(k)});return o})()`)
	labels := evalKindMaps(t, `(function(){var o={};`+kj+`.forEach(function(k){o[k]=kindLabel(k)});return o})()`)
	fallback := evalKindMaps(t, `({g: recordKind(undefined), g2: recordKind("nonsense"), g3: recordKind("toString")})`)
	if fallback["g"] != fallback["g2"] || fallback["g"] != fallback["g3"] {
		t.Fatalf("undefined/unknown kinds must share the fallback: %v", fallback)
	}
	seen := map[string]string{}
	for _, k := range kinds {
		g := glyphs[k]
		if g == "" || g == fallback["g"] {
			t.Errorf("kind %q has no distinct non-fallback glyph (%q)", k, g)
		}
		if prev, dup := seen[g]; dup {
			t.Errorf("kind %q shares glyph with %q", k, prev)
		}
		seen[g] = k
		if labels[k] == "" {
			t.Errorf("kind %q has no display word", k)
		}
	}
	if glyphs["outcome"] != "" || labels["outcome"] != "Outcome" || labels["decision"] != "Decision" {
		t.Errorf("outcome/decision mapping wrong: %q %v", glyphs["outcome"], labels)
	}
}
