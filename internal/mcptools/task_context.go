package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aoci-spec/aoci-code/internal/cognition"
	"github.com/aoci-spec/aoci-code/internal/config"
	"github.com/aoci-spec/aoci-code/internal/taskcontext"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type taskModeSession struct {
	callMu  sync.Mutex // serialize budget preflight and delivery within this server
	mu      sync.Mutex
	tasks   map[*mcp.ServerSession]string
	prelude map[*mcp.ServerSession]int
}

func newTaskModeSession() *taskModeSession {
	return &taskModeSession{tasks: map[*mcp.ServerSession]string{}, prelude: map[*mcp.ServerSession]int{}}
}

func (s *taskModeSession) addPrelude(owner *mcp.ServerSession, bytes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prelude[owner] += bytes
}

func (s *taskModeSession) takePrelude(owner *mcp.ServerSession) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.prelude[owner]
	delete(s.prelude, owner)
	return n
}

func (s *taskModeSession) active(owner *mcp.ServerSession) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tasks[owner]
}

func (s *taskModeSession) bind(owner *mcp.ServerSession, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[owner] = id
}

type taskContextKey struct{}

func taskIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(taskContextKey{}).(string)
	return id
}

func addTaskContextSchema(schemas mcpInputSchemas) {
	schema := schemas["aoci_get_entries"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	str := func() map[string]any { return map[string]any{"type": "string"} }
	stringsArray := func() map[string]any { return map[string]any{"type": "array", "items": str(), "maxItems": 256} }
	p := map[string]any{}
	for _, name := range []string{"action", "task_id", "goal", "event_id", "kind", "path", "query", "cursor", "note", "map_text"} {
		p[name] = str()
	}
	p["action"] = map[string]any{"type": "string", "enum": []string{"start", "resume", "status", "read", "search", "relations", "checkpoint", "map", "charge"}}
	for _, name := range []string{"limit_bytes", "budget_bytes", "external_bytes"} {
		p[name] = map[string]any{"type": "integer", "minimum": 0}
	}
	p["refs"] = stringsArray()
	p["pending"] = stringsArray()
	p["resolved"] = stringsArray()
	p["map_evidence"] = map[string]any{"type": "array", "maxItems": 256, "items": map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"kind", "ref", "sha256"},
		"properties": map[string]any{"kind": str(), "ref": str(), "sha256": str(), "start": map[string]any{"type": "integer", "minimum": 0}, "end": map[string]any{"type": "integer", "minimum": 0}},
	}}
	p["resolution_evidence"] = p["map_evidence"]
	properties["task"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"action"}, "properties": p}
}

func handleTaskContextRequest(root string, input getEntriesIn, mode *taskModeSession, call *mcp.CallToolRequest) *mcp.CallToolResult {
	if mode == nil {
		return errResult(errBadArgs, "task_context_mode_disabled", "Start the experimental server with mcp --task-context.")
	}
	if len(input.Paths) > 0 || len(input.ObjectRefs) > 0 || input.Dir != "" || input.VolumeID != "" {
		return errResult(errBadArgs, "task_context_mixed_read_inputs", "Use only task for a task-context request.")
	}
	loaded, fail := loadCognitionCtx(root)
	var set *cognition.Set
	if loaded != nil {
		set = loaded.set
	}
	if fail != nil && fail.Code == errNotInitialized {
		// A missing root index permits evidence gathering, not Bootstrap Apply.
		// A present but damaged/incomplete layout must still fail closed.
		cfg, err := config.LoadReadOnly(root)
		if err != nil {
			return errResult(errBadArgs, err.Error(), "")
		}
		if _, err := os.Lstat(filepath.Join(root, cfg.IndexPath)); !os.IsNotExist(err) {
			return failResult(fail)
		}
		set = &cognition.Set{Root: cognition.Asset{Descriptor: cognition.Descriptor{Path: cfg.IndexPath}}}
		fail = nil
	}
	if fail != nil {
		if taskControlRequest(*input.Task) {
			return renderTaskContextExecution(root, *input.Task, mode, call, true)
		}
		return failResult(fail)
	}
	if pending := pendingCognitionDeliveryFail(root, set); pending != nil {
		if taskControlRequest(*input.Task) {
			return renderTaskContextExecution(root, *input.Task, mode, call, true)
		}
		return failResult(pending)
	}
	return renderTaskContextExecution(root, *input.Task, mode, call, false)
}

func taskControlRequest(req taskcontext.Request) bool {
	return req.Action == "checkpoint" || req.Action == "status" || req.Action == "charge"
}

func renderTaskContextExecution(root string, input taskcontext.Request, mode *taskModeSession, call *mcp.CallToolRequest, controlOnly bool) *mcp.CallToolResult {
	var response *taskcontext.Response
	var err error
	if controlOnly {
		response, err = taskcontext.ExecuteControl(root, input)
	} else {
		response, err = taskcontext.Execute(root, input)
	}
	if err != nil {
		return errResult(errBadArgs, err.Error(), "Preserve the task ID; inspect task status or resume after an actual Host context reset.")
	}
	var owner *mcp.ServerSession
	if call != nil {
		owner = call.Session
	}
	mode.bind(owner, response.TaskID)
	data, err := json.Marshal(response)
	if err != nil {
		return errResult(errInternal, err.Error(), "")
	}
	return textResult(string(data))
}

// Only calls through this server are metered automatically. Other Host tools
// must be charged explicitly; their unknown costs are not a hard-cap claim.
func taskContextMeter(root string, mode *taskModeSession) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method == "tools/list" {
				mode.callMu.Lock()
				defer mode.callMu.Unlock()
				result, err := next(ctx, method, request)
				if err == nil {
					owner, _ := request.GetSession().(*mcp.ServerSession)
					data, _ := json.Marshal(result)
					if id := mode.active(owner); id != "" {
						if chargeErr := taskcontext.Charge(root, id, len(data)); chargeErr != nil {
							return nil, fmt.Errorf("task_metering_uncertain: tool list withheld because accounting failed: %w", chargeErr)
						}
					} else {
						mode.addPrelude(owner, len(data))
					}
				}
				return result, err
			}
			params, ok := request.GetParams().(*mcp.CallToolParamsRaw)
			if method != "tools/call" || !ok {
				return next(ctx, method, request)
			}
			mode.callMu.Lock()
			defer mode.callMu.Unlock()
			owner, _ := request.GetSession().(*mcp.ServerSession)
			id := mode.active(owner)
			var taskCall struct {
				Task *taskcontext.Request `json:"task"`
			}
			_ = json.Unmarshal(params.Arguments, &taskCall)
			isTask := params.Name == "aoci_get_entries" && taskCall.Task != nil
			if isTask {
				result, err := next(ctx, method, request)
				// Core preflights and meters its response. Inputs are a separate
				// explicit cost; charging never suppresses a completed operation.
				id = mode.active(owner)
				if id != "" {
					if e := taskcontext.Charge(root, id, len(params.Arguments)+mode.takePrelude(owner)); e != nil {
						return taskMeterWarning(result, e), err
					}
				}
				return result, err
			}
			if params.Name == "aoci_search" || params.Name == "aoci_get_entries" || params.Name == "aoci_header" {
				return errResult(errBadArgs, "task_context_bounded_read_required", "Use aoci_get_entries task search/read/relations; read kind=meta for the dictionary."), nil
			}
			if id == "" && params.Name != "aoci_rules" && params.Name != "aoci_overview" {
				return errResult(errBadArgs, "task_context_start_required", "Start or resume the task before continuing; formal recovery remains available through the ordinary CLI."), nil
			}
			remaining := 0
			if id != "" {
				var err error
				remaining, err = taskcontext.Remaining(root, id)
				if err != nil {
					return errResult(errBadArgs, err.Error(), ""), nil
				}
				// Read-only rules and Maintain planning can wait. Formal write
				// replies must remain visible, including when they exceed reserve.
				if remaining < 2048 && (params.Name == "aoci_rules" || params.Name == "aoci_maintain") {
					return errResult(errBadArgs, "task_budget_checkpoint_required", "Save a checkpoint; resume after a real new Host context, not another unchanged full read."), nil
				}
				ctx = context.WithValue(ctx, taskContextKey{}, id)
			}
			result, err := next(ctx, method, request)
			call, ok := result.(*mcp.CallToolResult)
			if !ok {
				return result, err
			}
			if id == "" {
				data, _ := json.Marshal(call)
				mode.addPrelude(owner, len(data)+len(params.Arguments))
				return result, err
			}
			if params.Name == "aoci_update_entry" && !call.IsError {
				if e := recordTaskApply(root, id, params.Arguments, call); e != nil {
					result = taskMeterWarning(result, e)
				}
			}
			data, marshalErr := json.Marshal(call)
			if marshalErr != nil {
				return taskMeterWarning(result, marshalErr), err
			}
			// Preserve the entire issued batch, never slice its candidates.
			// Large Maintain/rules responses are persisted and read via the
			// same budgeted reader. Actual write results are never hidden.
			if (params.Name == "aoci_maintain" || params.Name == "aoci_rules") && !taskResponseHasFormalWrites(call) && len(data) > min(taskcontext.DefaultPageBytes, remaining) {
				ref, e := taskcontext.SaveOutput(root, id, string(data))
				if e != nil {
					// A failed spool must never release the oversized body. No
					// formal write result is routed through this branch.
					call = errResult(errInternal, "task_output_persist_failed", "The full read result was withheld; repair task output storage before retrying. Do not author from a partial batch.")
				} else {
					deferred := map[string]any{"status": "output_deferred", "task_id": id, "result_ref": ref, "bytes": len(data), "next": "Read the complete retained response using task read kind=result path=result_ref; do not author or submit a partial candidate set."}
					if remainingTargets, verified := taskEmptyBudgetStop(call); verified {
						deferred["stop_summary"] = map[string]any{"result_status": "stopped", "cause_code": "task_budget_exhausted_before_batch", "candidate_count": 0, "current_batch_issued": false, "remaining_targets": remainingTargets}
						deferred["next_action"] = "checkpoint"
						deferred["full_result_read_required_for_next_action"] = false
						deferred["next"] = "Save a checkpoint now; no authoring batch was issued. After a real new Host context, resume this task and replan. The full diagnostic is retained; any later candidate batch must still be read completely."
					}
					out, _ := json.Marshal(deferred)
					call = textResult(string(out))
				}
				result = call
				data, _ = json.Marshal(call)
			}
			if e := taskcontext.Charge(root, id, len(data)+len(params.Arguments)); e != nil {
				result = taskMeterWarning(result, e)
			}
			return result, err
		}
	}
}

// Expose only a positively verified, empty budget stop. Missing fields,
// unknown failures and issued batches retain the full-read obligation.
func taskEmptyBudgetStop(call *mcp.CallToolResult) (int, bool) {
	if len(call.Content) != 1 || taskResponseHasFormalWrites(call) {
		return 0, false
	}
	content, ok := call.Content[0].(*mcp.TextContent)
	if !ok {
		return 0, false
	}
	var facts struct {
		Status       string             `json:"status"`
		Candidates   *[]json.RawMessage `json:"candidates"`
		CodePlan     json.RawMessage    `json:"code_plan"`
		DatabasePlan json.RawMessage    `json:"database_plan"`
		Batch        *struct {
			Included  *int `json:"included"`
			Remaining *int `json:"remaining"`
		} `json:"authoring_batch"`
		Stop *GlobalStopFacts `json:"stop"`
	}
	if json.Unmarshal([]byte(content.Text), &facts) != nil || facts.Status != autoStatusStopped || facts.Candidates == nil || len(*facts.Candidates) != 0 || facts.Batch == nil || facts.Batch.Included == nil || *facts.Batch.Included != 0 || facts.Batch.Remaining == nil || *facts.Batch.Remaining < 0 || facts.Stop == nil {
		return 0, false
	}
	for _, plan := range []json.RawMessage{facts.CodePlan, facts.DatabasePlan} {
		if len(plan) > 0 && string(plan) != "null" {
			return 0, false
		}
	}
	stop := facts.Stop
	return *facts.Batch.Remaining, stop.RuleCode == "task_batch_plan_stopped" && stop.Actual == "no_batch_issued" && stop.Cause == "task_budget_exhausted_before_batch"
}

func taskResponseHasFormalWrites(call *mcp.CallToolResult) bool {
	for _, content := range call.Content {
		text, ok := content.(*mcp.TextContent)
		if !ok {
			continue
		}
		var facts struct {
			Status              string `json:"status"`
			Applied             int    `json:"applied"`
			FormalWritesStarted bool   `json:"formal_writes_started"`
		}
		if json.Unmarshal([]byte(text.Text), &facts) == nil && (facts.FormalWritesStarted || facts.Applied > 0 || facts.Status == autoStatusApplied) {
			return true
		}
	}
	return false
}

// Only a successful formal handler can call this path. A task checkpoint's
// prose, new index bytes alone, or a saved candidate response is not an Apply.
func recordTaskApply(root, taskID string, arguments json.RawMessage, call *mcp.CallToolResult) error {
	var input updateEntryIn
	if json.Unmarshal(arguments, &input) != nil {
		return nil
	}
	batchID := input.CodeBatchID
	if batchID == "" {
		batchID = input.BatchID
	}
	if batchID == "" {
		return nil
	}
	code := input.Path != "" || strings.HasPrefix(input.ObjectRef, "code:")
	for _, item := range input.Entries {
		code = code || item.Path != "" || strings.HasPrefix(item.ObjectRef, "code:")
	}
	if !code {
		return nil
	}
	for _, content := range call.Content {
		text, ok := content.(*mcp.TextContent)
		if !ok {
			continue
		}
		var applied struct {
			Status  string `json:"status"`
			Applied int    `json:"applied"`
		}
		if json.Unmarshal([]byte(text.Text), &applied) != nil || applied.Status != autoStatusApplied || applied.Applied < 1 {
			continue
		}
		loaded, fail := loadCognitionCtx(root)
		if fail != nil {
			return fmt.Errorf("task_apply_receipt_not_recorded: %s", fail.Code)
		}
		return taskcontext.RecordApplied(root, taskID, batchID, loaded.set.CompositeIdentity)
	}
	return nil
}

func taskMeterWarning(result mcp.Result, err error) mcp.Result {
	if call, ok := result.(*mcp.CallToolResult); ok {
		call.Content = append(call.Content, &mcp.TextContent{Text: fmt.Sprintf("task_metering_uncertain: %s; preserve the original result and stop further reading until metering is repaired.", err)})
	}
	return result
}
