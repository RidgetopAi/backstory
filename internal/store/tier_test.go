package store

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestInsertRecordTierFromIdentity(t *testing.T) {
	cases := []struct {
		name     string
		kind     IdentityKind
		wantTier Tier
	}{
		{"human", IdentityHuman, TierHumanDeclared},
		{"agent", IdentityAgent, TierAgentDeclared},
		{"inference", IdentityInference, TierInferred},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
			id, err := s.InsertRecord(InsertRecordParams{
				Identity: Identity{Kind: tc.kind},
				Kind:     KindNote,
				Text:     "tier probe " + tc.name,
			})
			if err != nil {
				t.Fatalf("InsertRecord: %v", err)
			}
			rec, err := s.GetRecord(id)
			if err != nil {
				t.Fatalf("GetRecord: %v", err)
			}
			if rec.Tier != tc.wantTier {
				t.Errorf("tier for identity kind %v = %q, want %q", tc.kind, rec.Tier, tc.wantTier)
			}
		})
	}
}

// TestInsertRecordSignatureCarriesNoTier is the compile-level check the
// punch requires: it walks InsertRecordParams' exported fields by
// reflection and fails if any field is named Tier or has type store.Tier.
// A future edit that adds a caller-supplied tier field breaks this test,
// not just the trigger-mutation probe.
func TestInsertRecordSignatureCarriesNoTier(t *testing.T) {
	paramsType := reflect.TypeOf(InsertRecordParams{})
	tierType := reflect.TypeOf(Tier(""))

	for i := 0; i < paramsType.NumField(); i++ {
		f := paramsType.Field(i)
		if f.Name == "Tier" {
			t.Fatalf("InsertRecordParams has a field named Tier: %+v", f)
		}
		if f.Type == tierType || f.Type == reflect.PointerTo(tierType) {
			t.Fatalf("InsertRecordParams field %s has type Tier: %+v", f.Name, f)
		}
	}

	methodType := reflect.TypeOf((*Store)(nil)).Method(insertRecordMethodIndex(t)).Type
	// methodType.In(0) is the receiver; In(1) is InsertRecordParams, already
	// checked above field-by-field. Confirm the method takes exactly one
	// argument beyond the receiver, so a tier cannot have been smuggled in
	// as a second parameter.
	if methodType.NumIn() != 2 {
		t.Fatalf("InsertRecord takes %d arguments (plus receiver), want exactly 1 (InsertRecordParams)", methodType.NumIn()-1)
	}
}

func insertRecordMethodIndex(t *testing.T) int {
	t.Helper()
	m, ok := reflect.TypeOf((*Store)(nil)).MethodByName("InsertRecord")
	if !ok {
		t.Fatal("Store has no InsertRecord method")
	}
	return m.Index
}
