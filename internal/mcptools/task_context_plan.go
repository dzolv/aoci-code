package mcptools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoci-spec/aoci-code/internal/codebatch"
	"github.com/aoci-spec/aoci-code/internal/cognition"
	afs "github.com/aoci-spec/aoci-code/internal/fs"
	"github.com/aoci-spec/aoci-code/internal/taskcontext"
)

// taskCodePlanOptions is request-local. Remaining already protects the task's
// checkpoint/analysis reserve. Half of the material allowance is left for
// source and relation reading; the other half bounds the candidate response.
// No team configuration or default Maintain selection is changed.
func taskCodePlanOptions(root string, loaded *cognitionRepoCtx, all []codebatch.Candidate) (codebatch.Budget, codebatch.TaskOptions, error) {
	remaining, err := taskcontext.Remaining(root, loaded.taskContextID)
	if err != nil {
		return codebatch.Budget{}, codebatch.TaskOptions{}, err
	}
	sourceBudget := remaining / 2
	wireTotal := min(remaining-sourceBudget, maintainTransportBudget(loaded.cfg))
	// Reading-plan metadata accompanies each selected candidate; reserve its
	// bounded per-item cost inside the same wire estimate as candidate facts.
	wireBytes := wireTotal - fixedOverheadBytes(loaded.set) - sampleBytes(all) - 512
	if sourceBudget < 1 || wireBytes < 1 {
		return codebatch.Budget{}, codebatch.TaskOptions{}, fmt.Errorf("task_budget_exhausted_before_batch")
	}
	options := codebatch.TaskOptions{SourceBytes: map[string]int64{}, ReadingBudgetBytes: int64(sourceBudget),
		SegmentBytes: int64(min(sourceBudget, taskcontext.DefaultPageBytes))}
	refs := make([]string, 0, len(all))
	for _, candidate := range all {
		cost, err := taskCandidateSourceSize(root, candidate.Path)
		if err != nil {
			return codebatch.Budget{}, codebatch.TaskOptions{}, fmt.Errorf("task_candidate_source_unavailable: %s: %w", candidate.Path, err)
		}
		options.SourceBytes[candidate.ObjectRef] = cost
		refs = append(refs, candidate.ObjectRef)
	}
	options.Relations = cognition.TaskRelations(loaded.set, refs)
	base := codeCandidateBudget(loaded.cfg, loaded.set, all)
	wire := codebatch.Budget{Bytes: min(base.Bytes, wireBytes), Total: wireTotal,
		Size: func(candidate codebatch.Candidate) int {
			return base.Size(candidate) + len(candidate.ObjectRef) + 256
		}}
	return wire, options, nil
}

// Costs come only from regular, repository-relative paths. Lstat each
// component so a symlink/junction cannot redirect this planning probe outside
// the already-bound Managed Scope. The source body is never copied here.
func taskCandidateSourceSize(root, rel string) (int64, error) {
	normalized, err := afs.NormalizeRelPath(rel)
	if err != nil || normalized != rel {
		return 0, fmt.Errorf("task_source_path_unsafe")
	}
	current := filepath.Clean(root)
	parts := strings.Split(rel, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return 0, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (index < len(parts)-1 && !info.IsDir()) {
			return 0, fmt.Errorf("task_source_path_unsafe")
		}
		if index == len(parts)-1 {
			if !info.Mode().IsRegular() {
				return 0, fmt.Errorf("task_source_not_regular")
			}
			return info.Size(), nil
		}
	}
	return 0, fmt.Errorf("task_source_path_unsafe")
}

func stopCodeTaskPlan(result *volumeMaintainResult, err error) {
	result.Stop = &GlobalStopFacts{AffectedAsset: "code", Field: "task_batch_plan", RuleCode: "task_batch_plan_stopped",
		Expected: "complete_current_machine_batch_within_remaining_task_budget", Actual: "no_batch_issued",
		Cause: err.Error(), SafeNextAction: "checkpoint_or_resume_task_before_replanning"}
}
