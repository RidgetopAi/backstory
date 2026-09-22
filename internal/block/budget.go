package block

import (
	"strconv"
	"strings"

	"github.com/RidgetopAi/backstory/internal/store"
)

// DefaultBudgetTokens is the SessionStart block's default token budget when
// no settings row overrides it (AGENT-CONTRACT.md §The SessionStart block:
// "default ~1,500 tokens").
const DefaultBudgetTokens = 1500

// SettingBudgetKey is the settings key that holds the global SessionStart
// budget override, in tokens. A per-harness override lives at
// SettingBudgetKey + "." + <harness> (e.g. "sessionstart.budget_tokens.
// claude") and wins over this global key. Neither is ever a tool/CLI
// parameter (AGENT-CONTRACT.md §The never-list, item 5: settings are a
// human-only power).
const SettingBudgetKey = "sessionstart.budget_tokens"

// EstimateTokens is the named token estimator the budget is measured
// against: chars/4, the simplest estimator in the industry range
// AGENT-CONTRACT.md cites (Hermes ~2,200 chars, omp 5,000 tokens, Claude
// auto-memory 200 lines / 25 KB).
func EstimateTokens(s string) int {
	n := len([]rune(s))
	return (n + 3) / 4
}

// resolveBudget reads the effective budget for harness: the per-harness
// override when set, else the global setting, else DefaultBudgetTokens. A
// present-but-unparseable value is treated as absent rather than failing
// the whole render — a hook must never break a harness boot.
func resolveBudget(st *store.Store, harness string) (int, error) {
	if harness != "" {
		if n, ok, err := readBudgetSetting(st, SettingBudgetKey+"."+harness); err != nil {
			return 0, err
		} else if ok {
			return n, nil
		}
	}
	if n, ok, err := readBudgetSetting(st, SettingBudgetKey); err != nil {
		return 0, err
	} else if ok {
		return n, nil
	}
	return DefaultBudgetTokens, nil
}

func readBudgetSetting(st *store.Store, key string) (int, bool, error) {
	v, ok, err := st.GetSetting(key)
	if err != nil {
		return 0, false, err
	}
	if !ok {
		return 0, false, nil
	}
	n, convErr := strconv.Atoi(v)
	if convErr != nil {
		return 0, false, nil
	}
	return n, true, nil
}

// assemble joins the four data slots with the fixed final line and, if the
// result exceeds budgetTokens, cuts slot 2 (delta) first, then slot 4
// (attention), then slot 3 (coordination) — stopping as soon as the result
// fits (AGENT-CONTRACT.md §The SessionStart block's ordering). Slots 1
// (resume) and 5 (the final line) are never cut by this loop; only when
// slot 1 together with the final line still exceeds the budget is slot 1
// itself truncated to fit.
func assemble(slot1, slot2, slot3, slot4 string, budgetTokens int) string {
	slots := [5]string{slot1, slot2, slot3, slot4, FinalLine}
	join := func() string {
		var parts []string
		for _, s := range slots {
			if s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n\n")
	}

	if EstimateTokens(join()) <= budgetTokens {
		return join()
	}

	for _, i := range []int{1, 3, 2} { // slot2, slot4, slot3, in that cut priority
		if slots[i] == "" {
			continue
		}
		slots[i] = ""
		if EstimateTokens(join()) <= budgetTokens {
			return join()
		}
	}

	for slots[0] != "" && EstimateTokens(join()) > budgetTokens {
		r := []rune(slots[0])
		cut := len(r) - 8
		if cut < 0 {
			cut = 0
		}
		slots[0] = string(r[:cut])
	}
	return join()
}
