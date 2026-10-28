package codebatch

import (
	"reflect"
	"testing"

	"github.com/aoci-spec/aoci-code/internal/cognition"
)

func taskOptions(candidates []Candidate, cost, budget int64) TaskOptions {
	options := TaskOptions{SourceBytes: map[string]int64{}, ReadingBudgetBytes: budget, SegmentBytes: 64}
	for _, candidate := range candidates {
		options.SourceBytes[candidate.ObjectRef] = cost
	}
	return options
}

func TestTaskBatchPrioritizesOneHopAndPreservesDefaultOrder(t *testing.T) {
	candidates := budgetCandidates(12, "")
	options := taskOptions(candidates, 100, 250)
	options.FocusRefs = []string{"code:pkg/file_009.go"}
	options.Relations = []cognition.TaskRelation{
		{From: "code:pkg/file_009.go", To: "code:pkg/file_004.go", Resolved: true},
		{From: "code:pkg/file_004.go", To: "code:pkg/file_000.go", Resolved: true},
	}
	all, selected, reading, err := SelectTaskWithBudget(candidates, 10, Budget{}, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 12 || len(selected) != 2 || selected[0].Path != "pkg/file_004.go" || selected[1].Path != "pkg/file_009.go" {
		t.Fatalf("focus and one-hop neighbor, canonically sorted, not transitive closure: %#v", selected)
	}
	if reading.SelectedSourceBytes != 200 || reading.RemainingSourceBytes != 1000 || reading.RemainingTargets != 10 || reading.Oversize {
		t.Fatalf("reading costs and incompleteness must remain explicit: %#v", reading)
	}
	_, defaultSelected := SelectWithBudget(candidates, 2, Budget{})
	if defaultSelected[0].Path != "pkg/file_000.go" || defaultSelected[1].Path != "pkg/file_001.go" {
		t.Fatalf("opt-in grouping changed the default selector: %#v", defaultSelected)
	}
	_, again, _, err := SelectTaskWithBudget(all, 10, Budget{}, options)
	if err != nil || !reflect.DeepEqual(selected, again) {
		t.Fatalf("selection must be deterministic across input order: %v %#v", err, again)
	}
}

func TestTaskBatchBoundsWireCountAndSourceIndependently(t *testing.T) {
	candidates := budgetCandidates(10, "")
	for _, test := range []struct {
		name   string
		limit  int
		budget int64
		wire   Budget
		want   int
	}{
		{"reading", 10, 250, Budget{}, 2},
		{"wire", 10, 2000, Budget{Bytes: 150, Size: func(Candidate) int { return 100 }}, 1},
		{"count", 3, 2000, Budget{}, 3},
		{"exact", 10, 300, Budget{}, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, selected, _, err := SelectTaskWithBudget(candidates, test.limit, test.wire, taskOptions(candidates, 100, test.budget))
			if err != nil || len(selected) != test.want {
				t.Fatalf("independent %s bound: count=%d error=%v", test.name, len(selected), err)
			}
		})
	}
}

func TestTaskBatchDefaultAnchorUsesReverseAndUnindexedExplicitClues(t *testing.T) {
	candidates := budgetCandidates(5, "")
	options := taskOptions(candidates, 100, 200)
	options.Relations = []cognition.TaskRelation{
		// A missing index Entry does not make the canonical target ambiguous.
		{From: "code:pkg/file_004.go", To: "code:pkg/file_000.go", Token: "code:pkg/file_000.go"},
		{From: "code:pkg/file_004.go", To: "code:pkg/file_001.go", Resolved: true},
	}
	_, selected, _, err := SelectTaskWithBudget(candidates, 5, Budget{}, options)
	if err != nil || len(selected) != 2 || selected[0].Path != "pkg/file_000.go" || selected[1].Path != "pkg/file_004.go" {
		t.Fatalf("lexical anchor's explicit reverse clue comes before unrelated or second-hop candidates: %v %#v", err, selected)
	}
}

func TestTaskBatchOversizeSourceStillShipsAloneWithSegmentHints(t *testing.T) {
	candidates := budgetCandidates(3, "")
	options := taskOptions(candidates, 100, 50)
	options.SourceBytes["code:pkg/file_000.go"] = 301
	_, selected, reading, err := SelectTaskWithBudget(candidates, 10, Budget{}, options)
	if err != nil || len(selected) != 1 || !reading.Oversize || reading.SelectedSourceBytes != 301 || reading.RemainingTargets != 2 {
		t.Fatalf("oversize source must progress alone and leave remaining work explicit: %v %#v", err, reading)
	}
	if source := reading.Sources[0]; source.SegmentBytes != 50 || source.SegmentCount != 7 || !source.Oversize {
		t.Fatalf("segment hints must fit the reading budget without pretending the source fits: %#v", source)
	}
}

func TestTaskBatchRejectsUnknownCostsAndInvalidBudgets(t *testing.T) {
	candidates := budgetCandidates(2, "")
	options := taskOptions(candidates, 100, 200)
	delete(options.SourceBytes, "code:pkg/file_001.go")
	if _, _, _, err := SelectTaskWithBudget(candidates, 2, Budget{}, options); err == nil {
		t.Fatal("unknown source cost cannot be treated as zero")
	}
	options = taskOptions(candidates, 100, 0)
	if _, _, _, err := SelectTaskWithBudget(candidates, 2, Budget{}, options); err == nil {
		t.Fatal("reading budget must be explicit and positive")
	}
}

func TestTaskPlanKeepsCompleteReceiptAndAcceptsNonPrefixSubmission(t *testing.T) {
	root := t.TempDir()
	candidates := budgetCandidates(12, "")
	options := taskOptions(candidates, 100, 200)
	options.FocusRefs = []string{"code:pkg/file_009.go"}
	options.Relations = []cognition.TaskRelation{{From: "code:pkg/file_009.go", To: "code:pkg/file_004.go", Resolved: true}}
	plan, reading, err := BuildTaskPlanWithBudget(root, hexIdentity("1"), hexIdentity("2"), "aoci.code.txt", hexIdentity("3"), candidates, 10, Budget{Total: 4096}, options)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Included != 2 || plan.Remaining != 10 || plan.TransportBudgetBytes != 4096 || reading.RemainingTargets != 10 {
		t.Fatalf("issued plan must retain complete remaining work: %#v %#v", plan, reading)
	}
	receipt, err := LoadReceipt(root, plan.BatchID)
	if err != nil || len(receipt.AllTargets) != 12 || len(receipt.Targets) != 2 {
		t.Fatalf("complete all-target and selected-target bindings: %v %#v", err, receipt)
	}
	defaultPlan, err := BuildPlanWithBudget(root, hexIdentity("1"), hexIdentity("2"), "aoci.code.txt", hexIdentity("3"), candidates, 10, Budget{})
	if err != nil || defaultPlan.PlanID != plan.PlanID || defaultPlan.BatchID == plan.BatchID {
		t.Fatalf("same complete plan, distinct task batch: %v %#v", err, defaultPlan)
	}
	submissions := []Submission{}
	for _, candidate := range plan.Candidates {
		submissions = append(submissions, Submission{ObjectRef: candidate.ObjectRef, CandidateID: candidate.CandidateID, SourceSHA256: candidate.SourceSHA256})
	}
	if _, err := ValidateSubmission(root, plan.BatchID, hexIdentity("1"), hexIdentity("2"), "aoci.code.txt", hexIdentity("3"), submissions, false); err != nil {
		t.Fatalf("exact non-prefix machine batch must validate: %v", err)
	}
	if _, err := ValidateSubmission(root, plan.BatchID, hexIdentity("1"), hexIdentity("2"), "aoci.code.txt", hexIdentity("3"), submissions[:1], false); err == nil {
		t.Fatal("Host cannot submit a sliced subset of the issued task batch")
	}
}
