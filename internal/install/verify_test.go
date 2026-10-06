package install

import (
	"testing"

	"github.com/RidgetopAi/backstory/internal/block"
)

// TestVerifyStdoutOK is the punch's clause 5 unit-level proof for the
// classification verifyHook applies to a zero-exit hook's stdout: exercised
// directly, without a subprocess or a daemon, so the three success shapes
// and their negatives are each pinned down in isolation.
func TestVerifyStdoutOK(t *testing.T) {
	tests := []struct {
		name      string
		out       string
		daemonUp  bool
		wantValid bool
	}{
		{
			name:      "rendered block ending in the final line",
			out:       "Resume: did the thing\n\n" + block.FinalLineFor("rec-1"),
			daemonUp:  true,
			wantValid: true,
		},
		{
			name:      "exact empty-project line",
			out:       block.EmptyProjectLine,
			daemonUp:  true,
			wantValid: true,
		},
		{
			name:      "empty stdout with no daemon reachable",
			out:       "",
			daemonUp:  false,
			wantValid: true,
		},
		{
			name:      "empty stdout while a daemon IS reachable",
			out:       "",
			daemonUp:  true,
			wantValid: false,
		},
		{
			name:      "unrelated text",
			out:       "TOTAL GARBAGE: not a block, not the empty-state line",
			daemonUp:  true,
			wantValid: false,
		},
		{
			name:      "unrelated text with no daemon reachable",
			out:       "TOTAL GARBAGE",
			daemonUp:  false,
			wantValid: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := verifyStdoutOK(tt.out, tt.daemonUp); got != tt.wantValid {
				t.Errorf("verifyStdoutOK(%q, daemonUp=%v) = %v, want %v", tt.out, tt.daemonUp, got, tt.wantValid)
			}
		})
	}
}
