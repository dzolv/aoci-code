package mcptools

import (
	"fmt"

	"github.com/aoci-spec/aoci-code/textassets"
)

// The optional mode replaces the reading contract rather than appending a
// contradictory exception to the full-system contract. Formal authoring and
// governance still use their existing machine-issued plans and safeguards.
func taskContextRules(locale string) string {
	return taskContextMessage(locale, "mcp.task_context.rules")
}

func taskContextDescriptions(descriptions mcpToolDescriptions, locale string) {
	keys := map[textassets.ID]string{
		textassets.ContractMCPRulesDescription:      "mcp.task_context.description.rules",
		textassets.ContractMCPGetEntriesDescription: "mcp.task_context.description.get_entries",
		textassets.ContractMCPSearchDescription:     "mcp.task_context.description.search",
		textassets.ContractMCPOverviewDescription:   "mcp.task_context.description.overview",
		textassets.ContractMCPHeaderDescription:     "mcp.task_context.description.header",
		textassets.ContractMaintainToolDescription:  "mcp.task_context.description.maintain",
	}
	for id, key := range keys {
		descriptions[id] = taskContextMessage(locale, key)
	}
}

// Bind rendering to the requested locale, independently of process-global
// locale selection. Catalog failures remain explicit instead of falling back.
func taskContextMessage(locale, key string) string {
	value, err := textassets.Message(locale, key)
	if err != nil {
		return fmt.Sprintf("[text_asset_error:%s]", key)
	}
	return value
}
