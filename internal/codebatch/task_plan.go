package codebatch

import (
	"fmt"
	"sort"

	"github.com/aoci-spec/aoci-code/internal/cognition"
)

// TaskOptions opts one request into task-oriented batching. Costs are source
// byte counts keyed by canonical ObjectRef, measured by the caller from the
// current source. They are planning hints, not proof that evidence was read.
// Neither these options nor relation hints change the authoring contract.
type TaskOptions struct {
	FocusRefs          []string
	Relations          []cognition.TaskRelation
	SourceBytes        map[string]int64
	ReadingBudgetBytes int64
	SegmentBytes       int64
}

type TaskSourceReading struct {
	ObjectRef    string `json:"object_ref"`
	SourceBytes  int64  `json:"source_bytes"`
	SegmentBytes int64  `json:"segment_bytes"`
	SegmentCount int64  `json:"segment_count"`
	Oversize     bool   `json:"oversize"`
}

// TaskReadingPlan reports costs and suggested bounded source reads. The
// selection budget is not a process memory cap; a caller still has to perform
// and verify the real reads. RemainingTargets prevents a small batch being
// mistaken for a complete task or relation closure.
type TaskReadingPlan struct {
	ReadingBudgetBytes   int64               `json:"reading_budget_bytes"`
	SelectedSourceBytes  int64               `json:"selected_source_bytes"`
	RemainingSourceBytes int64               `json:"remaining_source_bytes"`
	RemainingTargets     int                 `json:"remaining_targets"`
	Oversize             bool                `json:"oversize"`
	Sources              []TaskSourceReading `json:"sources"`
}

// SelectTaskWithBudget selects a complete machine batch using source costs in
// addition to the existing count and wire bounds. Exact focus candidates come
// first, followed by their explicit one-hop neighbors; unrelated candidates
// retain lexical order. With no focus, the first pending candidate anchors the
// group. Only this opt-in selector uses relation hints. All returned candidates
// are canonically sorted so existing receipt and submission rules still apply.
func SelectTaskWithBudget(candidates []Candidate, limit int, wire Budget, task TaskOptions) (ordered, selected []Candidate, reading TaskReadingPlan, err error) {
	ordered, _ = Select(candidates, 0)
	reading = TaskReadingPlan{ReadingBudgetBytes: task.ReadingBudgetBytes, Sources: []TaskSourceReading{}}
	if task.ReadingBudgetBytes < 1 || task.SegmentBytes < 1 {
		return nil, nil, reading, fmt.Errorf("code_task_reading_budget_invalid")
	}
	if err := validateCandidates(ordered); err != nil {
		return nil, nil, reading, err
	}
	for _, candidate := range ordered {
		cost, ok := task.SourceBytes[candidate.ObjectRef]
		if !ok || cost < 0 {
			return nil, nil, reading, fmt.Errorf("code_task_source_cost_unavailable: %s", candidate.ObjectRef)
		}
		if cost > int64(^uint64(0)>>1)-reading.RemainingSourceBytes {
			return nil, nil, reading, fmt.Errorf("code_task_source_cost_overflow")
		}
		reading.RemainingSourceBytes += cost
	}
	focus := map[string]bool{}
	for _, ref := range task.FocusRefs {
		focus[ref] = true
	}
	if len(focus) == 0 && len(ordered) > 0 {
		focus[ordered[0].ObjectRef] = true
	}
	neighbors := map[string]bool{}
	for _, relation := range task.Relations {
		// Explicit unresolved canonical targets may identify a new candidate;
		// ambiguous name tokens never identify or rank an object.
		if relation.To != "" {
			if focus[relation.From] {
				neighbors[relation.To] = true
			}
			if focus[relation.To] {
				neighbors[relation.From] = true
			}
		}
	}
	ranked := cloneCandidates(ordered)
	rank := func(ref string) int {
		if focus[ref] {
			return 0
		}
		if neighbors[ref] {
			return 1
		}
		return 2
	}
	sort.SliceStable(ranked, func(i, j int) bool { return rank(ranked[i].ObjectRef) < rank(ranked[j].ObjectRef) })
	usedWire := 0
	for _, candidate := range ranked {
		if limit > 0 && len(selected) >= limit {
			break
		}
		cost := task.SourceBytes[candidate.ObjectRef]
		wireCost := 0
		if wire.Bytes > 0 && wire.Size != nil {
			wireCost = wire.Size(candidate)
			if wireCost < 0 {
				return nil, nil, reading, fmt.Errorf("code_task_wire_cost_invalid")
			}
		}
		if len(selected) > 0 && (cost > task.ReadingBudgetBytes-reading.SelectedSourceBytes ||
			(wire.Bytes > 0 && wire.Size != nil && wireCost > wire.Bytes-usedWire)) {
			break
		}
		selected = append(selected, candidate)
		reading.SelectedSourceBytes += cost
		reading.RemainingSourceBytes -= cost
		usedWire += wireCost
		// An oversize candidate makes progress alone. Suggested segments are
		// bounded even though the complete source exceeds one reading budget.
		segmentBytes := min(task.SegmentBytes, task.ReadingBudgetBytes)
		segmentCount := cost / segmentBytes
		if cost%segmentBytes != 0 {
			segmentCount++
		}
		oversize := cost > task.ReadingBudgetBytes
		reading.Oversize = reading.Oversize || oversize
		reading.Sources = append(reading.Sources, TaskSourceReading{ObjectRef: candidate.ObjectRef,
			SourceBytes: cost, SegmentBytes: segmentBytes, SegmentCount: segmentCount, Oversize: oversize})
	}
	selected, _ = Select(selected, 0)
	sort.Slice(reading.Sources, func(i, j int) bool { return reading.Sources[i].ObjectRef < reading.Sources[j].ObjectRef })
	reading.RemainingTargets = len(ordered) - len(selected)
	return ordered, selected, reading, nil
}

// BuildTaskPlanWithBudget reuses the existing complete-plan receipt issuer.
// AllTargets remains the whole pending set; only the machine-issued Targets
// subset changes. No Host slicing or alternative submission path is needed.
func BuildTaskPlanWithBudget(root, compositeIdentity, scopePolicyIdentity, codeVolumePath, codeVolumeSHA256 string, candidates []Candidate, limit int, wire Budget, task TaskOptions) (Plan, TaskReadingPlan, error) {
	if limit < 1 {
		return Plan{}, TaskReadingPlan{}, fmt.Errorf("code_candidate_batch_limit_invalid")
	}
	ordered, selected, reading, err := SelectTaskWithBudget(candidates, limit, wire, task)
	if err != nil {
		return Plan{}, reading, err
	}
	plan, err := savePlan(root, compositeIdentity, scopePolicyIdentity, codeVolumePath, codeVolumeSHA256, ordered, selected, limit)
	if err != nil {
		return Plan{}, reading, err
	}
	plan.TransportBudgetBytes = wire.Total
	if plan.TransportBudgetBytes == 0 {
		plan.TransportBudgetBytes = wire.Bytes
	}
	return plan, reading, nil
}
