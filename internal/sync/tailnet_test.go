package sync

import "testing"

func TestParseTailnetNames(t *testing.T) {
	got := parseTailnetNames([]byte(`{"Self":{"DNSName":"desk.tail1234.ts.net."}}`))
	if len(got) != 2 || got[0] != "desk.tail1234.ts.net" || got[1] != "desk" {
		t.Fatalf("got %v", got)
	}
	if parseTailnetNames([]byte(`{"Self":{}}`)) != nil || parseTailnetNames([]byte(`nope`)) != nil {
		t.Fatal("want nil for no name / bad json")
	}
}

func TestBackCandidatesOrderDedupeValidate(t *testing.T) {
	req := message{BackUser: "u", BackNames: []string{"a.ts.net", "a", "", "a", "x y", "10.0.0.1"}}
	got := BackCandidates(req, "10.0.0.1")
	want := []string{"u@a.ts.net", "u@a", "u@10.0.0.1"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if c := BackCandidates(message{BackUser: "u", BackHost: "h", BackNames: []string{"a"}}, "1.2.3.4"); len(c) != 1 || c[0] != "h" {
		t.Fatalf("explicit: %v", c)
	}
}
