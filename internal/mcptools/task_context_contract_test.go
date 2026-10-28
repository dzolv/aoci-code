package mcptools

import (
	"strings"
	"testing"

	"github.com/aoci-spec/aoci-code/textassets"
)

func TestTaskContextContractKeepsBoundariesInBothLocales(t *testing.T) {
	for _, locale := range []string{textassets.DefaultLocale, textassets.LegacyLocale} {
		rules := taskContextRules(locale)
		for _, token := range []string{"context_mode=task_context", "aoci_get_entries", `"action":"start"`, `"action":"resume"`, "goal", "task_id", "event_id", "status", "read", "search", "relations", "checkpoint", "map", "charge", "map_text", "map_evidence", "external_bytes", "next_cursor", "Root/Meta", "CAS", "Recovery", "candidate_id", "source_sha256", "Attestation"} {
			if !strings.Contains(rules, token) {
				t.Errorf("%s contract is missing %q", locale, token)
			}
		}
	}
}

func TestTaskContextDescriptionsOnlyReplaceReadingContract(t *testing.T) {
	for _, locale := range []string{textassets.DefaultLocale, textassets.LegacyLocale} {
		values := mcpToolDescriptions{textassets.ContractMCPUpdateDescription: "existing update contract", textassets.ContractMCPReportDescription: "existing report contract", textassets.ContractMCPRemoveDescription: "existing removal contract"}
		taskContextDescriptions(values, locale)
		if len(values) != 9 || values[textassets.ContractMCPUpdateDescription] != "existing update contract" || values[textassets.ContractMCPReportDescription] != "existing report contract" || values[textassets.ContractMCPRemoveDescription] != "existing removal contract" {
			t.Fatalf("%s modified unrelated tool contracts: %#v", locale, values)
		}
		for _, id := range []textassets.ID{textassets.ContractMCPRulesDescription, textassets.ContractMCPGetEntriesDescription, textassets.ContractMCPOverviewDescription, textassets.ContractMCPHeaderDescription, textassets.ContractMCPSearchDescription, textassets.ContractMaintainToolDescription} {
			if strings.TrimSpace(values[id]) == "" {
				t.Errorf("%s empty task description %s", locale, id)
			}
		}
	}
}

func TestTaskContextContractsRenderRequestedLocaleFromAssets(t *testing.T) {
	previous := textassets.ActiveLocale()
	t.Cleanup(func() { _ = textassets.SetActiveLocale(previous) })
	for _, locale := range []string{textassets.DefaultLocale, textassets.LegacyLocale} {
		other := textassets.DefaultLocale
		if locale == other {
			other = textassets.LegacyLocale
		}
		if err := textassets.SetActiveLocale(other); err != nil {
			t.Fatal(err)
		}
		want, err := textassets.Message(locale, "mcp.task_context.rules")
		if err != nil || taskContextRules(locale) != want {
			t.Fatalf("requested rules locale %s not rendered from its asset: %v", locale, err)
		}
		values := mcpToolDescriptions{}
		taskContextDescriptions(values, locale)
		for id, key := range map[textassets.ID]string{
			textassets.ContractMCPRulesDescription:      "mcp.task_context.description.rules",
			textassets.ContractMCPGetEntriesDescription: "mcp.task_context.description.get_entries",
			textassets.ContractMCPSearchDescription:     "mcp.task_context.description.search",
			textassets.ContractMCPOverviewDescription:   "mcp.task_context.description.overview",
			textassets.ContractMCPHeaderDescription:     "mcp.task_context.description.header",
			textassets.ContractMaintainToolDescription:  "mcp.task_context.description.maintain",
		} {
			want, err := textassets.Message(locale, key)
			if err != nil || values[id] != want {
				t.Errorf("requested description locale %s for %s not rendered from its asset: %v", locale, id, err)
			}
		}
	}
	if got := taskContextRules("unsupported"); got != "[text_asset_error:mcp.task_context.rules]" {
		t.Fatalf("unsupported locale silently fell back: %q", got)
	}
}
