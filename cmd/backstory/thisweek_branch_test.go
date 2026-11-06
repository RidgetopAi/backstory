package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/week"
)

// this-week --json carries the handoff's branch, and omits the key when
// there is none (task 64cd2b5e).
func TestProjectSummaryJSONHandoffBranch(t *testing.T) {
	with, err := json.Marshal(projectSummary(week.ProjectSummary{ProjectKey: "k", HandoffBranch: "feature-a"}))
	if err != nil || !strings.Contains(string(with), `"handoff_branch":"feature-a"`) {
		t.Errorf("with branch: %s, %v", with, err)
	}
	without, err := json.Marshal(projectSummary(week.ProjectSummary{ProjectKey: "k"}))
	if err != nil || strings.Contains(string(without), "handoff_branch") {
		t.Errorf("without branch: %s, %v", without, err)
	}
}
