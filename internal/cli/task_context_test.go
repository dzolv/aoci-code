package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aoci-spec/aoci-code/textassets"
	"github.com/spf13/cobra"
)

func TestTaskContextCLIRequiresExplicitOptIn(t *testing.T) {
	for _, command := range []*cobra.Command{newMCPCmd(), newIndexAgentGuideCmd(), newCodexCompactCmd(nil)} {
		flag := command.Flags().Lookup("task-context")
		if flag == nil || flag.DefValue != "false" {
			t.Fatalf("%s must expose an opt-in task-context flag: %#v", command.Name(), flag)
		}
	}
}

func TestTaskContextCompactHookBypassesRepositoryAndNamesResume(t *testing.T) {
	previous := textassets.ActiveLocale()
	t.Cleanup(func() { _ = textassets.SetActiveLocale(previous) })
	for _, locale := range []string{textassets.DefaultLocale, textassets.LegacyLocale} {
		if err := textassets.SetActiveLocale(locale); err != nil {
			t.Fatal(err)
		}
		command := newCodexCompactCmd(bytes.NewReader(make([]byte, 16)))
		root := &cobra.Command{Use: "aoci", PersistentPreRunE: func(*cobra.Command, []string) error {
			t.Fatal("task compact hook inspected repository gate")
			return nil
		}}
		root.AddCommand(command)
		root.SetArgs([]string{"codex-compact", "--task-context"})
		var output bytes.Buffer
		root.SetOut(&output)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		for _, token := range []string{"event_id: aoci-compact-", "aoci_get_entries", `"action":"resume"`, "task_id", "aoci_rules", "checkpoint", "Recovery"} {
			if !strings.Contains(output.String(), token) {
				t.Errorf("%s task compact hook missing %q: %s", locale, token, output.String())
			}
		}
		if strings.Contains(output.String(), `refresh_reasons=["context_compaction"]`) {
			t.Fatal("task recovery must not inherit automatic full refresh")
		}
	}
}

func TestTaskContextGuidePreservesGovernanceStops(t *testing.T) {
	previous := textassets.ActiveLocale()
	t.Cleanup(func() { _ = textassets.SetActiveLocale(previous) })
	root, _ := buildManualAtomicEntriesRepo(t)
	read := func(task bool) volumeAgentGuide {
		args := []string{"--json", "index", "agent", "guide", "--agent", "codex"}
		if task {
			args = append(args, "--task-context")
		}
		output := mustCLI(t, root, args...)
		var guide volumeAgentGuide
		if err := json.Unmarshal([]byte(output), &guide); err != nil {
			t.Fatal(err)
		}
		return guide
	}
	before, after := read(false), read(true)
	if before.ContextMode != "" || after.ContextMode != "task_context" || !strings.HasSuffix(after.Commands.Guide, " --task-context") || !strings.Contains(strings.Join(after.Instructions, " "), "aoci_get_entries") {
		t.Fatalf("missing explicit task entry: before=%+v after=%+v", before, after)
	}
	// Remove exactly the new reading guidance: the governance result must be
	// byte-for-byte structurally unchanged, including its migration stop.
	after.ContextMode = ""
	after.Commands.Guide = strings.TrimSuffix(after.Commands.Guide, " --task-context")
	after.Instructions = after.Instructions[1:]
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("task reading changed governance: before=%+v after=%+v", before, after)
	}
}
