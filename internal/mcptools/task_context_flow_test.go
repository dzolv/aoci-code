package mcptools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoci-spec/aoci-code/internal/taskcontext"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectTaskClient(t *testing.T, root string) *mcp.ClientSession {
	t.Helper()
	server, err := NewServerWithOptions(root, "task-flow-test", ServerOptions{TaskContext: true})
	if err != nil {
		t.Fatal(err)
	}
	return connectTaskClientToServer(t, server)
}

func connectTaskClientToServer(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "task-flow-test", Version: "test"}, nil)
	cs, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func taskCall(t *testing.T, client *mcp.ClientSession, request taskcontext.Request) taskcontext.Response {
	t.Helper()
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "aoci_get_entries", Arguments: map[string]any{"task": request}})
	if err != nil {
		t.Fatal(err)
	}
	text := resText(t, result)
	if result.IsError {
		t.Fatalf("task %s failed: %s", request.Action, text)
	}
	var response taskcontext.Response
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("task response: %v: %.1000s", err, text)
	}
	return response
}

func taskMaintain(t *testing.T, client *mcp.ClientSession, id string) volumeMaintainResult {
	t.Helper()
	raw := callVolumeTool(t, client, "aoci_maintain", map[string]any{"scope": "code"})
	var deferred struct {
		Status string `json:"status"`
		Ref    string `json:"result_ref"`
	}
	if err := json.Unmarshal([]byte(raw), &deferred); err != nil {
		t.Fatalf("Maintain response: %v: %.1000s", err, raw)
	}
	if deferred.Status == "output_deferred" {
		var body strings.Builder
		cursor := ""
		for pages := 0; ; pages++ {
			if pages > 100 {
				t.Fatal("result paging did not converge")
			}
			page := taskCall(t, client, taskcontext.Request{Action: "read", TaskID: id, Kind: "result", Path: deferred.Ref, Cursor: cursor})
			for _, item := range page.Items {
				body.WriteString(item.Text)
			}
			cursor = page.NextCursor
			if cursor == "" {
				break
			}
		}
		var stored struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal([]byte(body.String()), &stored); err != nil {
			t.Fatalf("stored result: %v", err)
		}
		if len(stored.Content) != 1 {
			t.Fatalf("unexpected retained content: %+v", stored)
		}
		raw = stored.Content[0].Text
	}
	var result volumeMaintainResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("Maintain decode: %v: %.1000s", err, raw)
	}
	if result.CodePlan == nil {
		t.Fatalf("no issued batch: %.1800s", raw)
	}
	return result
}

func TestTaskContextOptInKeepsNineToolsAndDefaultSchemas(t *testing.T) {
	root := buildLargeCodeCandidateRepo(t, 1)
	for _, enabled := range []bool{false, true} {
		var client *mcp.ClientSession
		if enabled {
			client = connectTaskClient(t, root)
		} else {
			client = connectMCPClient(t, root)
		}
		listed, err := client.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(listed.Tools) != 9 {
			t.Fatalf("tools=%d", len(listed.Tools))
		}
		for _, tool := range listed.Tools {
			if tool.Name == "aoci_get_entries" {
				data, _ := json.Marshal(tool.InputSchema)
				if strings.Contains(string(data), `"task"`) != enabled {
					t.Fatalf("opt-in schema mismatch: %s", data)
				}
			}
		}
	}
}

func TestTaskContextToolListCostsTransferAndAccountingFailureIsVisible(t *testing.T) {
	root := buildLargeCodeCandidateRepo(t, 1)
	client := connectTaskClient(t, root)
	listed, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	listBytes, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	started := taskCall(t, client, taskcontext.Request{Action: "start", Goal: "account for startup material"})
	status := taskCall(t, client, taskcontext.Request{Action: "status", TaskID: started.TaskID})
	if status.State.UsedBytes < len(listBytes) {
		t.Fatalf("startup list omitted from accounting: used=%d list=%d", status.State.UsedBytes, len(listBytes))
	}
	// Corrupt this disposable task's state to exercise a real failed charge.
	statePath := filepath.Join(root, ".aoci", "task-context", started.TaskID+".json")
	if err := os.WriteFile(statePath, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTools(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "task_metering_uncertain") {
		t.Fatalf("tool list silently ignored failed accounting: %v", err)
	}
}

func TestTaskContextBuildAcrossServerRestartWithoutOverview(t *testing.T) {
	root := buildLargeCodeCandidateRepo(t, 3)
	setTeamCodeBatchEntries(t, root, 2)
	client := connectTaskClient(t, root)
	started := taskCall(t, client, taskcontext.Request{Action: "start", Goal: "index the fixture incrementally", BudgetBytes: 256 * 1024})
	id := started.TaskID
	page := taskCall(t, client, taskcontext.Request{Action: "read", TaskID: id, Kind: "source", Path: "generated/0000.go"})
	if len(page.Items) != 1 || page.Items[0].Evidence == nil {
		t.Fatalf("source evidence missing: %+v", page)
	}
	taskCall(t, client, taskcontext.Request{Action: "map", TaskID: id, MapText: "The generated package supplies independent fixture constants; remaining modules are not yet examined.", MapEvidence: []taskcontext.EvidenceRef{*page.Items[0].Evidence}})
	taskCall(t, client, taskcontext.Request{Action: "checkpoint", TaskID: id, Note: "finish the current batch", Pending: []string{"check cross-batch relationships"}})
	first := taskMaintain(t, client, id)
	if len(first.Candidates) != 2 {
		t.Fatalf("first batch=%d", len(first.Candidates))
	}
	for _, candidate := range first.Candidates {
		taskCall(t, client, taskcontext.Request{Action: "read", TaskID: id, Kind: "source", Path: candidate.Path})
	}
	result := applyVolumeBatch(t, client, codeBatchArguments(wireCodePlan(first)))
	if result.Status != autoStatusApplied || result.Applied != 2 {
		t.Fatalf("first Apply: %+v", result)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	client = connectTaskClient(t, root) // new server, no cached delivery or active task
	resumed := taskCall(t, client, taskcontext.Request{Action: "resume", TaskID: id, EventID: "fresh-host-context-2"})
	if resumed.State.PendingCount != 1 || !resumed.State.MapValid {
		t.Fatalf("lost checkpoint/map: %+v", resumed)
	}
	second := taskMaintain(t, client, id)
	if len(second.Candidates) != 1 || second.Candidates[0].Path != "generated/0002.go" {
		t.Fatalf("wrong remaining batch: %+v", second.Candidates)
	}
	taskCall(t, client, taskcontext.Request{Action: "read", TaskID: id, Kind: "source", Path: second.Candidates[0].Path})
	last := applyVolumeBatch(t, client, codeBatchArguments(wireCodePlan(second)))
	if last.Status != autoStatusApplied || last.Applied != 1 || !last.Aligned {
		t.Fatalf("last Apply: %+v", last)
	}
	state := taskCall(t, client, taskcontext.Request{Action: "status", TaskID: id})
	if state.State.CompletedBatchCount != 2 || state.State.LastBatchID != second.CodePlan.BatchID {
		t.Fatalf("formal Apply progress missing: %+v", state.State)
	}
	if state.State.PendingCount != 1 {
		t.Fatal("successful writes silently discarded pending review")
	}
	ledger, err := os.ReadFile(filepath.Join(root, ".aoci", "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ledger), `"op":"overview"`) {
		t.Fatal("task construction invoked a full Overview")
	}
}

func TestTaskContextSourceFirstAndRulesWithoutIndex(t *testing.T) {
	root := buildVolumeRepo(t, true, false)
	if err := os.Remove(filepath.Join(root, "aoci.txt")); err != nil {
		t.Fatal(err)
	}
	client := connectTaskClient(t, root)
	rules := callVolumeTool(t, client, "aoci_rules", map[string]any{})
	if !strings.Contains(rules, "context_mode=task_context") {
		t.Fatalf("source-first rules unavailable: %.500s", rules)
	}
	started := taskCall(t, client, taskcontext.Request{Action: "start", Goal: "read evidence before building an index"})
	page := taskCall(t, client, taskcontext.Request{Action: "read", TaskID: started.TaskID, Kind: "source", Path: "main.go"})
	if len(page.Items) != 1 || page.Items[0].Evidence == nil {
		t.Fatalf("source-first read failed: %+v", page)
	}
}

func TestTaskContextSpoolFailureNeverReleasesUnboundedBody(t *testing.T) {
	root := buildVolumeRepo(t, true, false)
	started, err := taskcontext.Execute(root, taskcontext.Request{Action: "start", Goal: "bounded output failure"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".aoci", "task-context", "outputs"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	mode := newTaskModeSession()
	mode.bind(nil, started.TaskID)
	large := `{"status":"candidates","payload":"` + strings.Repeat("secret-body-marker-", 10000) + `"}`
	handler := taskContextMeter(root, mode)(func(context.Context, string, mcp.Request) (mcp.Result, error) { return textResult(large), nil })
	request := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: "aoci_maintain", Arguments: json.RawMessage(`{}`)}}
	before, err := taskcontext.Remaining(root, started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := handler(context.Background(), "tools/call", request)
	if err != nil {
		t.Fatal(err)
	}
	call := result.(*mcp.CallToolResult)
	body := resText(t, call)
	if !call.IsError || !strings.Contains(body, "task_output_persist_failed") || strings.Contains(body, "secret-body-marker") || len(body) > 1024 {
		t.Fatalf("unbounded spool failure: %.1500s", body)
	}
	after, err := taskcontext.Remaining(root, started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if after >= before {
		t.Fatal("failure response was not metered")
	}
}

func TestTaskContextSessionsDoNotShareActiveTask(t *testing.T) {
	root := buildLargeCodeCandidateRepo(t, 1)
	server, err := NewServerWithOptions(root, "task-session-test", ServerOptions{TaskContext: true})
	if err != nil {
		t.Fatal(err)
	}
	a := connectTaskClientToServer(t, server)
	b := connectTaskClientToServer(t, server)
	sa := taskCall(t, a, taskcontext.Request{Action: "start", Goal: "task A"})
	result, err := b.CallTool(context.Background(), &mcp.CallToolParams{Name: "aoci_maintain", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(resText(t, result), "task_context_start_required") {
		t.Fatal("second Host inherited first Host's task")
	}
	sb := taskCall(t, b, taskcontext.Request{Action: "start", Goal: "task B"})
	if sa.TaskID == sb.TaskID {
		t.Fatal("tasks unexpectedly shared identity")
	}
	before, err := taskcontext.Remaining(root, sa.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	callVolumeTool(t, b, "aoci_rules", map[string]any{})
	after, err := taskcontext.Remaining(root, sa.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("other Host response charged to wrong task")
	}
}

func TestTaskContextRecoveryPreservesCheckpointWithoutDeliveringEvidence(t *testing.T) {
	root := buildVolumeRepo(t, true, false)
	client := connectTaskClient(t, root)
	started := taskCall(t, client, taskcontext.Request{Action: "start", Goal: "recover interrupted work"})
	if err := os.WriteFile(filepath.Join(root, "aoci.txt.aoci-cas.intent"), []byte("pending"), 0600); err != nil {
		t.Fatal(err)
	}
	checkpoint := taskCall(t, client, taskcontext.Request{Action: "checkpoint", TaskID: started.TaskID, Note: "preserve the outstanding write", Pending: []string{"resume the formal transaction"}})
	if checkpoint.Verification != "cached_unverified" || checkpoint.State.MapValid {
		t.Fatalf("checkpoint misstates reliability: %+v", checkpoint)
	}
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "aoci_get_entries", Arguments: map[string]any{"task": taskcontext.Request{Action: "read", TaskID: started.TaskID, Kind: "source", Path: "main.go"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("pending recovery allowed a new evidence delivery")
	}
	status := taskCall(t, client, taskcontext.Request{Action: "status", TaskID: started.TaskID})
	if status.State.PendingCount != 1 || status.Verification != "cached_unverified" {
		t.Fatalf("checkpoint lost during recovery: %+v", status)
	}
}

func TestTaskContextMeterKeepsWriteFailureAndBoundsExtraReads(t *testing.T) {
	root := buildLargeCodeCandidateRepo(t, 1)
	client := connectTaskClient(t, root)
	started := taskCall(t, client, taskcontext.Request{Action: "start", Goal: "budget guard"})
	if err := taskcontext.Charge(root, started.TaskID, taskcontext.DefaultBudgetBytes); err != nil {
		t.Fatal(err)
	}
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "aoci_maintain", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(resText(t, result), "task_budget_checkpoint_required") {
		t.Fatalf("unbounded planning: %s", resText(t, result))
	}
	// A formal write rejection must remain visible even after the budget ends.
	result, err = client.CallTool(context.Background(), &mcp.CallToolParams{Name: "aoci_update_entry", Arguments: map[string]any{"entries": []any{}}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || strings.Contains(resText(t, result), "task_budget_checkpoint_required") {
		t.Fatalf("write response hidden: %s", resText(t, result))
	}
}

func TestTaskContextBudgetStopCanCheckpointWithoutReadingLargeDiagnostic(t *testing.T) {
	root := buildLargeCodeCandidateRepo(t, 5)
	client := connectTaskClient(t, root)
	started := taskCall(t, client, taskcontext.Request{Action: "start", Goal: "stop safely before another batch"})
	remaining, err := taskcontext.Remaining(root, started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := taskcontext.Charge(root, started.TaskID, remaining-3000); err != nil {
		t.Fatal(err)
	}
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "aoci_maintain", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	raw := resText(t, result)
	var deferred struct {
		Status   string `json:"status"`
		Ref      string `json:"result_ref"`
		Next     string `json:"next_action"`
		Required bool   `json:"full_result_read_required_for_next_action"`
		Stop     *struct {
			Issued    bool `json:"current_batch_issued"`
			Remaining int  `json:"remaining_targets"`
		} `json:"stop_summary"`
	}
	if err := json.Unmarshal([]byte(raw), &deferred); err != nil {
		t.Fatal(err)
	}
	if result.IsError || deferred.Status != "output_deferred" || deferred.Ref == "" || deferred.Next != "checkpoint" || deferred.Required || deferred.Stop == nil || deferred.Stop.Issued || deferred.Stop.Remaining != 5 || len(raw) > 1024 {
		t.Fatalf("budget stop did not remain actionable: %s", raw)
	}
	stored, err := os.ReadFile(filepath.Join(root, ".aoci", "task-context", "outputs", started.TaskID, deferred.Ref+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) <= len(raw) || !strings.Contains(string(stored), "task_budget_exhausted_before_batch") {
		t.Fatal("complete diagnostic not preserved")
	}
	checkpoint := taskCall(t, client, taskcontext.Request{Action: "checkpoint", TaskID: started.TaskID, Note: "No batch issued; resume after a genuine new Host context."})
	if checkpoint.Status != "checkpoint_saved" || checkpoint.State.CompletedBatchCount != 0 {
		t.Fatalf("checkpoint lost: %+v", checkpoint)
	}
}

func TestTaskContextStopProjectionRejectsAmbiguousOrWritableResults(t *testing.T) {
	base := `{"status":"stopped","candidates":[],"authoring_batch":{"included":0,"remaining":5},"stop":{"rule_code":"task_batch_plan_stopped","actual":"no_batch_issued","cause":"task_budget_exhausted_before_batch"}}`
	if remaining, ok := taskEmptyBudgetStop(textResult(base)); !ok || remaining != 5 {
		t.Fatal("valid empty stop not recognized")
	}
	for _, raw := range []string{
		strings.Replace(base, `"candidates":[]`, `"candidates":null`, 1),
		strings.Replace(base, `"candidates":[],`, ``, 1),
		strings.Replace(base, `"candidates":[]`, `"candidates":[{}]`, 1),
		strings.Replace(base, `"candidates":[]`, `"candidates":{}`, 1),
		strings.Replace(base, `"included":0,`, ``, 1),
		strings.Replace(base, `"remaining":5`, `"remaining":-1`, 1),
		strings.Replace(base, `"status":"stopped"`, `"status":"stopped","code_plan":{}`, 1),
		strings.Replace(base, `"status":"stopped"`, `"status":"stopped","database_plan":{}`, 1),
		strings.Replace(base, `"status":"stopped"`, `"status":"stopped","formal_writes_started":true`, 1),
		strings.Replace(base, `task_budget_exhausted_before_batch`, `task_source_path_unsafe`, 1),
	} {
		if _, ok := taskEmptyBudgetStop(textResult(raw)); ok {
			t.Fatalf("unsafe read exemption: %s", raw)
		}
	}
}
