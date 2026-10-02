package panel

import (
	"encoding/json"
	"testing"
)

// modelJSPathForRuntime is js/model.js's own path, run under Node the same
// way launcher_contract_test.go and groups_selection_test.go already run
// js/launchers.js and js/groups.js (js_runtime_test.go's evalJS) — DONE
// WHEN clause 7: "Every js/model.js list accessor returns [] for an
// absent field; a JS test calls each on {} and asserts an array."
const modelJSPathForRuntime = "js/model.js"

// TestModelListAccessorsReturnArrayForAbsentField is DONE WHEN clause 7
// itself. Round 3's desk log flooded with `TypeError: Cannot read property
// 'length' of undefined` from exactly this gap (js/model.js's own doc
// comment) — every list accessor must return an array, never `undefined`,
// when the field it reads is absent.
func TestModelListAccessorsReturnArrayForAbsentField(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{"topAttention", "topAttention({})"},
		{"topWhereLeftOff", "topWhereLeftOff({})"},
		{"topWeek", "topWeek({})"},
		{"attentionEvidenceIds", "attentionEvidenceIds({})"},
		{"rowChildren", "rowChildren({})"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw := evalJS(t, c.expr, modelJSPathForRuntime)
			var got interface{}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("decode %s result %s: %v", c.expr, raw, err)
			}
			arr, ok := got.([]interface{})
			if !ok {
				t.Fatalf("%s = %s (%T), want an array", c.expr, raw, got)
			}
			if len(arr) != 0 {
				t.Fatalf("%s = %s, want an empty array", c.expr, raw)
			}
		})
	}
}

// TestModelShortName: decision 63ce9687 P4 — one helper drops a leading
// "projects/" from display names; everything else is unchanged and the
// result is never emptied.
func TestModelShortName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"projects/foo", "foo"},
		{"projects", "projects"},
		{"projects/", "projects/"},
		{"work/foo", "work/foo"},
		{"", ""},
	}
	for _, c := range cases {
		in, _ := json.Marshal(c.in)
		raw := evalJS(t, "shortName("+string(in)+")", modelJSPathForRuntime)
		var got string
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		if got != c.want {
			t.Errorf("shortName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
