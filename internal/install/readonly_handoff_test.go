package install

import (
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/block"
)

// TestStubLinesSayReadOnlySessionKeepsHandoff: both stub lines carry the
// shared read-only clause (task df6646f5).
func TestStubLinesSayReadOnlySessionKeepsHandoff(t *testing.T) {
	for name, line := range map[string]string{"claude": StubLine, "agents": AgentsStubLine} {
		if !strings.Contains(line, block.ReadOnlyHandoffClause) {
			t.Errorf("%s stub line lacks %q: %s", name, block.ReadOnlyHandoffClause, line)
		}
	}
}
