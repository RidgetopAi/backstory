package panel

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

// recordsJSPath is the one file in this plugin allowed to read a
// `backstory records --json` field (task 04d28c19; js/records.js's own doc
// comment). The goldens are the SAME files cmd/backstory's records tests
// assert byte-for-byte, not copies.
const recordsJSPath = "js/records.js"

var recordsGoldenPaths = []string{
	"../cmd/backstory/testdata/records/default.json.golden",
	"../cmd/backstory/testdata/records/history.json.golden",
}

func loadRecordsGoldenKeys(t *testing.T, transform func(string) string) map[string]bool {
	t.Helper()
	keys := map[string]bool{}
	for _, p := range recordsGoldenPaths {
		raw, err := os.ReadFile(p) //nolint:gosec // p is one of this file's own hardcoded golden paths
		if err != nil {
			t.Fatalf("read golden %s: %v", p, err)
		}
		var decoded interface{}
		if err := json.Unmarshal([]byte(transform(string(raw))), &decoded); err != nil {
			t.Fatalf("unmarshal golden %s: %v", p, err)
		}
		collectJSONKeys(decoded, keys)
	}
	return keys
}

func recordsFields(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(recordsJSPath)
	if err != nil {
		t.Fatalf("read %s: %v", recordsJSPath, err)
	}
	return extractFieldAccesses(string(raw))
}

// TestRecordsFieldContract: every field js/records.js reads must exist in
// the records goldens, so a contract change turns this red.
func TestRecordsFieldContract(t *testing.T) {
	fields := recordsFields(t)
	if len(fields) < 7 {
		t.Fatalf("extracted only %d field accesses from %s; expected at least 7 — extraction likely broken", len(fields), recordsJSPath)
	}
	golden := loadRecordsGoldenKeys(t, func(s string) string { return s })
	var missing []string
	for f := range fields {
		if !golden[f] {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%s reads field(s) not present in the records goldens: %v", recordsJSPath, missing)
	}
}

// TestRecordsFieldContractCatchesRename proves the check is not vacuous: a
// golden with `tier` renamed no longer satisfies what records.js reads.
func TestRecordsFieldContractCatchesRename(t *testing.T) {
	fields := recordsFields(t)
	if !fields["tier"] {
		t.Fatalf("expected %s to read tier", recordsJSPath)
	}
	mutated := loadRecordsGoldenKeys(t, func(s string) string {
		out := strings.ReplaceAll(s, `"tier"`, `"tierName"`)
		if out == s {
			t.Fatalf("mutation was a no-op")
		}
		return out
	})
	if mutated["tier"] {
		t.Fatalf("rename did not take")
	}
}
