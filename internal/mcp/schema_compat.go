package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// inputSchemaShape is the subset of a JSON Schema object CheckAdditiveOnly
// inspects: which properties exist, what each looks like, and which are
// required.
type inputSchemaShape struct {
	Properties map[string]json.RawMessage `json:"properties"`
	Required   []string                   `json:"required"`
}

// CheckAdditiveOnly reports whether candidate is an additive-only evolution
// of frozen (AGENT-CONTRACT.md §The five tools: "additive-only thereafter"):
//   - every tool in frozen must still exist in candidate;
//   - every property in a frozen tool's schema must still exist in
//     candidate's schema for that tool, byte-identical;
//   - a frozen tool's required list must be unchanged in candidate.
//
// candidate may add brand-new (necessarily optional, since required is
// pinned) properties to a tool's schema without failing this check.
func CheckAdditiveOnly(frozen, candidate []Tool) error {
	byName := make(map[string]Tool, len(candidate))
	for _, t := range candidate {
		byName[t.Name] = t
	}

	for _, ft := range frozen {
		ct, ok := byName[ft.Name]
		if !ok {
			return fmt.Errorf("tool %q removed", ft.Name)
		}

		var fs, cs inputSchemaShape
		if err := json.Unmarshal(ft.InputSchema, &fs); err != nil {
			return fmt.Errorf("tool %q: parse frozen schema: %w", ft.Name, err)
		}
		if err := json.Unmarshal(ct.InputSchema, &cs); err != nil {
			return fmt.Errorf("tool %q: parse candidate schema: %w", ft.Name, err)
		}

		if !stringSetEqual(fs.Required, cs.Required) {
			return fmt.Errorf("tool %q: required fields changed: %v -> %v", ft.Name, fs.Required, cs.Required)
		}

		for prop, frozenSchema := range fs.Properties {
			candidateSchema, ok := cs.Properties[prop]
			if !ok {
				return fmt.Errorf("tool %q: property %q removed", ft.Name, prop)
			}
			equal, err := jsonEqual(frozenSchema, candidateSchema)
			if err != nil {
				return fmt.Errorf("tool %q: property %q: %w", ft.Name, prop, err)
			}
			if !equal {
				return fmt.Errorf("tool %q: property %q changed", ft.Name, prop)
			}
		}
	}
	return nil
}

func stringSetEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa, sb := append([]string{}, a...), append([]string{}, b...)
	sort.Strings(sa)
	sort.Strings(sb)
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}

// jsonEqual reports whether two JSON documents are equal after
// canonicalisation (key order and whitespace ignored).
func jsonEqual(a, b json.RawMessage) (bool, error) {
	ca, err := canonicalizeJSON(a)
	if err != nil {
		return false, err
	}
	cb, err := canonicalizeJSON(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ca, cb), nil
}

// canonicalizeJSON reparses arbitrary JSON into Go values and re-marshals
// it: encoding/json always sorts map keys and emits deterministic
// whitespace, so two documents that differ only in key order or formatting
// canonicalize to identical bytes.
func canonicalizeJSON(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("mcp: canonicalize json: %w", err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("mcp: canonicalize json: %w", err)
	}
	return b, nil
}
