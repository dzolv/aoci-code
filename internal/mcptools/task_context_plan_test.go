package mcptools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoci-spec/aoci-code/internal/baseline"
	"github.com/aoci-spec/aoci-code/internal/codebatch"
	"github.com/aoci-spec/aoci-code/internal/cognition"
	"github.com/aoci-spec/aoci-code/internal/config"
	"github.com/aoci-spec/aoci-code/internal/taskcontext"
	"github.com/aoci-spec/aoci-code/internal/volumegovernance"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func taskPlanClient(t *testing.T, root string) *mcp.ClientSession {
	t.Helper()
	server, err := NewServerWithOptions(root, "task-plan-test", ServerOptions{TaskContext: true})
	if err != nil {
		t.Fatal(err)
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "task-plan-test", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func taskPlanMaintain(t *testing.T, session *mcp.ClientSession, taskID string) volumeMaintainResult {
	t.Helper()
	raw := callVolumeTool(t, session, "aoci_maintain", map[string]any{"scope": "code"})
	var deferred struct {
		Status string `json:"status"`
		Ref    string `json:"result_ref"`
	}
	if err := json.Unmarshal([]byte(raw), &deferred); err != nil {
		t.Fatal(err)
	}
	if deferred.Status == "output_deferred" {
		var output strings.Builder
		cursor := ""
		for {
			pageRaw := callVolumeTool(t, session, "aoci_get_entries", map[string]any{"task": map[string]any{
				"action": "read", "task_id": taskID, "kind": "result", "path": deferred.Ref, "cursor": cursor,
			}})
			var page taskcontext.Response
			if err := json.Unmarshal([]byte(pageRaw), &page); err != nil || len(page.Items) != 1 {
				t.Fatalf("complete retained Maintain page unavailable: %v %s", err, pageRaw)
			}
			output.WriteString(page.Items[0].Text)
			cursor = page.NextCursor
			if cursor == "" {
				break
			}
		}
		var retained struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal([]byte(output.String()), &retained); err != nil || len(retained.Content) != 1 {
			t.Fatalf("retained response must remain complete: %v", err)
		}
		raw = retained.Content[0].Text
	}
	var result volumeMaintainResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTaskMaintainIssuesNonPrefixReceiptAndCompleteBatchApplies(t *testing.T) {
	root := buildVolumeRepo(t, true, false)
	volumePath := filepath.Join(root, "aoci.code.txt")
	volume, err := os.ReadFile(volumePath)
	if err != nil {
		t.Fatal(err)
	}
	volume = []byte(strings.ReplaceAll(string(volume), "R:aoci.meta.txt", "R:code:src/z.go"))
	if err := os.WriteFile(volumePath, volume, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadBase(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := baseline.Snapshot(root, cfg.WalkOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := baseline.Save(root, baseline.NewBaseline(snapshot)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"main.go", "src/a.go", "src/z.go"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte("package main\n//"+strings.Repeat("x", 17000)+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	session := taskPlanClient(t, root)
	callVolumeTool(t, session, "aoci_get_entries", map[string]any{"task": map[string]any{
		"action": "start", "task_id": "batch-test", "goal": "update related fixture sources", "event_id": "host-1",
	}})
	maintain := taskPlanMaintain(t, session, "batch-test")
	if maintain.Stop != nil || maintain.CodePlan == nil || maintain.TaskReading == nil || len(maintain.Candidates) != 2 ||
		maintain.Candidates[0].Path != "main.go" || maintain.Candidates[1].Path != "src/z.go" {
		t.Fatalf("bounded task batch should group anchor and one-hop relation, skipping lexical middle: %#v", maintain)
	}
	receipt, err := codebatch.LoadReceipt(root, maintain.CodePlan.BatchID)
	if err != nil || len(receipt.AllTargets) != 3 || len(receipt.Targets) != 2 || maintain.TaskReading.RemainingTargets != 1 {
		t.Fatalf("task receipt retains full pending set: %v %#v", err, receipt)
	}
	applied := applyVolumeBatch(t, session, codeBatchArguments(wireCodePlan(maintain)))
	if applied.Status != autoStatusApplied || applied.Applied != 2 {
		t.Fatalf("complete non-prefix machine batch must apply through ordinary receipt validation: %#v", applied)
	}
	currentCfg, err := config.LoadBase(root)
	if err != nil || currentCfg.MaintainTransportBudget() != cfg.MaintainTransportBudget() || codeBatchLimit(currentCfg) != codeBatchLimit(cfg) {
		t.Fatal("request-local budgeting changed team defaults")
	}
}

func TestTaskMaintainBudgetStopDoesNotIssueEmptyOrAlternativeBatch(t *testing.T) {
	root := buildLargeCodeCandidateRepo(t, 3)
	if _, err := taskcontext.Execute(root, taskcontext.Request{Action: "start", TaskID: "exhausted", Goal: "test exhausted plan"}); err != nil {
		t.Fatal(err)
	}
	remaining, err := taskcontext.Remaining(root, "exhausted")
	if err != nil {
		t.Fatal(err)
	}
	if err := taskcontext.Charge(root, "exhausted", remaining-1000); err != nil {
		t.Fatal(err)
	}
	loaded, fail := loadCognitionCtx(root)
	if fail != nil {
		t.Fatal(fail)
	}
	loaded.taskContextID = "exhausted"
	facts, err := volumegovernance.Assess(root, loaded.cfg, loaded.set)
	if err != nil {
		t.Fatal(err)
	}
	result := volumeMaintainResult{Governance: facts}
	buildVolumeCodeCandidates(root, loaded, &result, volumegovernance.CodeAuthoringWorkFor(loaded.set, facts.CodeDrift))
	if result.Stop == nil || result.CodePlan != nil || len(result.Candidates) != 0 || result.TaskReading != nil {
		t.Fatalf("exhausted planning must be an explicit zero-issuance stop: %#v", result)
	}
	files, err := filepath.Glob(filepath.Join(root, ".aoci", "drafts", "code-cognition", "candidate-*.json"))
	if err != nil || len(files) != 0 {
		t.Fatalf("failed planning created a receipt: %v %v", err, files)
	}
}

func TestTaskSourceCostsRejectUnsafePathsAndDefaultMaintainIsUnchanged(t *testing.T) {
	root := buildLargeCodeCandidateRepo(t, 3)
	for _, path := range []string{"../escape.go", "generated", "missing.go"} {
		if _, err := taskCandidateSourceSize(root, path); err == nil {
			t.Fatalf("unsafe/nonregular/missing source cost accepted: %s", path)
		}
	}
	maintain := maintainVolumeBatch(t, connectMCPClient(t, root))
	if maintain.TaskReading != nil || maintain.CodePlan == nil || len(maintain.Candidates) != 3 || maintain.Candidates[0].Path != "generated/0000.go" {
		t.Fatalf("default Maintain must preserve old lexical selection and response: %#v", maintain)
	}
	if maintain.Candidates[0].Domain != cognition.ScopeCode {
		t.Fatal("unexpected candidate domain")
	}
}
