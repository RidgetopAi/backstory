package recall

import (
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

// A handoff shows its branch in recall's rendering when known (task 64cd2b5e).
func TestRenderItemShowsHandoffBranch(t *testing.T) {
	it := Item{ID: "12345678-aaaa", Kind: store.KindHandoff, Status: StatusCurrent, Text: "t", GitBranch: "feature-a"}
	if got := renderItem(it, AltitudeHeadline); !strings.Contains(got, "on branch feature-a") {
		t.Errorf("render = %q, want the branch", got)
	}
	it.GitBranch = ""
	if got := renderItem(it, AltitudeHeadline); strings.Contains(got, "branch") {
		t.Errorf("render = %q, want no branch wording", got)
	}
}
