package taskcontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aoci-spec/aoci-code/internal/config"
	"github.com/aoci-spec/aoci-code/internal/machinecontract"
	"github.com/aoci-spec/aoci-code/internal/managedscope"
)

func write(t *testing.T, root, path, text string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}
func call(t *testing.T, root string, req Request) *Response {
	t.Helper()
	if req.TaskID == "" {
		req.TaskID = "task"
	}
	r, err := Execute(root, req)
	if err != nil {
		t.Fatalf("%s/%s: %v", req.Action, req.Kind, err)
	}
	return r
}
func start(t *testing.T, root string) *Response {
	return call(t, root, Request{Action: "start", Goal: "test evidence-driven work", EventID: "host-1"})
}

func TestTaskSurvivesReloadAndRequiresActuallyReadMapEvidence(t *testing.T) {
	root := t.TempDir()
	write(t, root, "main.go", "package main\n")
	r := start(t, root)
	if r.Status != "map_missing" || r.State.MapValid {
		t.Fatalf("unexpected start: %#v", r)
	}
	fake := EvidenceRef{Kind: "source", Ref: "main.go", SHA256: digest([]byte("package main\n")), End: 13}
	if _, err := Execute(root, Request{Action: "map", TaskID: "task", MapText: "A program", MapEvidence: []EvidenceRef{fake}}); err == nil {
		t.Fatal("accepted evidence never delivered")
	}
	read := call(t, root, Request{Action: "read", Kind: "source", Path: "main.go"})
	ev := *read.Items[0].Evidence
	call(t, root, Request{Action: "map", MapText: "The program source is main.go. Its behavior needs investigation.", MapEvidence: []EvidenceRef{ev}})
	call(t, root, Request{Action: "checkpoint", Note: "done", Pending: []string{"verify behavior"}})
	// Execute loads JSON afresh on every call: no process-global state is needed.
	status := call(t, root, Request{Action: "resume", EventID: "host-2"})
	if status.State.MapVersion != 1 || !status.State.MapValid || status.State.PendingCount != 1 || status.State.Progress != 1 {
		t.Fatalf("state lost: %#v", status.State)
	}
	if status.Status == "done" {
		t.Fatal("Host prose became formal completion")
	}
	note := call(t, root, Request{Action: "status", Kind: "checkpoint"})
	if note.Items[0].Text != "done" {
		t.Fatal("checkpoint lost")
	}
	if _, err := os.Stat(filepath.Join(root, "aoci.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("task wrote formal index")
	}
}

func TestUTF8PagingCountsSerializedResponsesAndRetainsCursor(t *testing.T) {
	root := t.TempDir()
	text := strings.Repeat("汉字🙂\"\n<>&", 3000)
	write(t, root, "unicode.txt", text)
	initial := start(t, root)
	total := initial.State.UsedBytes
	got := ""
	cursor := ""
	for i := 0; i < 100; i++ {
		r := call(t, root, Request{Action: "read", Kind: "source", Path: "unicode.txt", Cursor: cursor, LimitBytes: 2048})
		b, _ := json.Marshal(r)
		if len(b) > 2048 {
			t.Fatalf("page %d too large: %d", i, len(b))
		}
		total += len(b)
		if r.State.UsedBytes != total {
			t.Fatalf("serialized bytes not counted: got %d want %d", r.State.UsedBytes, total)
		}
		if r.Status == "budget_stop" || r.Status == "capacity_stop" {
			break
		}
		if !utf8.ValidString(r.Items[0].Text) {
			t.Fatal("split UTF8 rune")
		}
		got += r.Items[0].Text
		cursor = r.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(got) == 0 || !strings.HasPrefix(text, got) {
		t.Fatal("paging changed source bytes")
	}
	if cursor == "" && got != text {
		t.Fatal("paging silently dropped source")
	}
	s, err := load(root, "task")
	if err != nil {
		t.Fatal(err)
	}
	if s.Used > s.Budget-ReserveBytes {
		t.Fatal("material consumed checkpoint reserve")
	}
	if cursor != "" {
		before := len(got)
		call(t, root, Request{Action: "checkpoint", Note: "continue Unicode source", Pending: []string{"remaining source"}})
		call(t, root, Request{Action: "resume", EventID: "host-2"})
		r := call(t, root, Request{Action: "read", Kind: "source", Path: "unicode.txt", Cursor: cursor, LimitBytes: 2048})
		if r.Items[0].Evidence.Start != before {
			t.Fatal("resume reread initial page")
		}
	}
}

func TestSourceDriftInvalidatesOnlyRelatedEvidenceAndMap(t *testing.T) {
	root := t.TempDir()
	write(t, root, "one.go", strings.Repeat("a", 10000))
	write(t, root, "two.go", "two")
	start(t, root)
	one := call(t, root, Request{Action: "read", Kind: "source", Path: "one.go", LimitBytes: 2048})
	two := call(t, root, Request{Action: "read", Kind: "source", Path: "two.go"})
	call(t, root, Request{Action: "map", MapText: "One and two are the two inspected sources.", MapEvidence: []EvidenceRef{*one.Items[0].Evidence, *two.Items[0].Evidence}})
	write(t, root, "one.go", strings.Repeat("b", 10000))
	r := call(t, root, Request{Action: "status"})
	if r.State.MapValid || r.State.EvidenceCount != 1 || r.State.InvalidEvidenceCount != 1 || r.State.PendingCount != 1 {
		t.Fatalf("wrong invalidation: %#v", r.State)
	}
	if _, err := Execute(root, Request{Action: "read", TaskID: "task", Kind: "source", Path: "one.go", Cursor: one.NextCursor}); err == nil {
		t.Fatal("source drift accepted old cursor")
	}
	call(t, root, Request{Action: "resume", EventID: "host-2"})
	r = call(t, root, Request{Action: "read", Kind: "source", Path: "one.go"})
	if r.State.Progress != 3 {
		t.Fatalf("rechecked evidence not progress: %#v", r.State)
	}
}

func legacy(root string, entries ...string) string {
	return "#legacy\n===Sources" + filepath.ToSlash(root) + "/===\n" + strings.Join(entries, "\n") + "\n"
}
func TestIndexGrowthRetainsReadWorkAndChangedEntryIsRechecked(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "a")
	line := "a.go[CD9S]: F:alpha | R:- | A:- | S:-"
	write(t, root, "aoci.txt", legacy(root, line))
	start(t, root)
	search := call(t, root, Request{Action: "search", Query: "alpha"})
	if len(search.Items) != 1 {
		t.Fatalf("fixture entry missing: %#v", search)
	}
	ref := search.Items[0].Ref
	call(t, root, Request{Action: "read", Kind: "entry", Refs: []string{ref}})
	call(t, root, Request{Action: "read", Kind: "source", Path: "a.go"})
	write(t, root, "aoci.txt", legacy(root, line, "b.go[CD9S]: F:beta | R:- | A:- | S:-"))
	status := call(t, root, Request{Action: "resume", EventID: "host-2"})
	if status.State.EvidenceCount != 2 || status.State.InvalidEvidenceCount != 0 {
		t.Fatalf("index addition discarded evidence: %#v", status.State)
	}
	write(t, root, "aoci.txt", legacy(root, strings.Replace(line, "alpha", "changed", 1)))
	status = call(t, root, Request{Action: "status"})
	if status.State.EvidenceCount != 1 || status.State.InvalidEvidenceCount != 1 {
		t.Fatalf("entry drift not isolated: %#v", status.State)
	}
}

func TestResumeIsIdempotentAndNotesCannotManufactureProgress(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "a")
	start(t, root)
	if _, err := Execute(root, Request{Action: "resume", TaskID: "task"}); err == nil {
		t.Fatal("resume omitted Host event")
	}
	first := call(t, root, Request{Action: "resume", EventID: "host-2"})
	if first.State.PageBytes != DefaultPageBytes/2 {
		t.Fatal("no progress did not shrink page")
	}
	replay := call(t, root, Request{Action: "resume", EventID: "host-2"})
	if replay.State.Phase != first.State.Phase || replay.State.UsedBytes <= first.State.UsedBytes {
		t.Fatal("replay reset phase budget")
	}
	call(t, root, Request{Action: "checkpoint", Note: "done; all work complete", Pending: []string{"still unresolved"}})
	call(t, root, Request{Action: "resume", EventID: "host-3"})
	r := call(t, root, Request{Action: "resume", EventID: "host-4"})
	if r.Status != "no_progress_stop" || r.State.Progress != 0 {
		t.Fatal("arbitrary prose advanced work")
	}
	r = call(t, root, Request{Action: "read", Kind: "source", Path: "a.go"})
	if len(r.Items) != 0 || r.Status != "no_progress_stop" {
		t.Fatal("loop stop still delivered material")
	}
	old := call(t, root, Request{Action: "resume", EventID: "host-2"})
	if old.State.Phase != r.State.Phase {
		t.Fatal("older event reset budget")
	}
}

func TestPathBoundariesAndUnindexedSources(t *testing.T) {
	root := t.TempDir()
	write(t, root, "ok.go", "okay")
	write(t, root, ".env", "SECRET")
	write(t, root, "node_modules/hidden.js", "hidden")
	start(t, root)
	r := call(t, root, Request{Action: "search", Kind: "source", Query: "ok"})
	if len(r.Items) != 1 || r.Items[0].Ref != "ok.go" {
		t.Fatal("unindexed source unavailable")
	}
	for _, path := range []string{"../escape", "C:/outside", "/outside", ".env", "node_modules/hidden.js", ".aoci/task-context/task.json"} {
		if _, err := Execute(root, Request{Action: "read", TaskID: "task", Kind: "source", Path: path}); err == nil {
			t.Errorf("unsafe path accepted: %s", path)
		}
	}
	outside := t.TempDir()
	write(t, outside, "secret.go", "outside")
	if err := os.Symlink(filepath.Join(outside, "secret.go"), filepath.Join(root, "link.go")); err == nil {
		if _, err = Execute(root, Request{Action: "read", TaskID: "task", Kind: "source", Path: "link.go"}); err == nil {
			t.Fatal("symlink accepted")
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err == nil {
		if _, err = Execute(root, Request{Action: "read", TaskID: "task", Kind: "source", Path: "linked/secret.go"}); err == nil {
			t.Fatal("symlink ancestor accepted")
		}
	}
}

func TestChargePreservesOverdrawnWriteAccountingAndCheckpoint(t *testing.T) {
	root := t.TempDir()
	start(t, root)
	before, _ := Remaining(root, "task")
	if err := Charge(root, "task", 1234); err != nil {
		t.Fatal(err)
	}
	after, _ := Remaining(root, "task")
	if before-after != 1234 {
		t.Fatal("same-server output not charged")
	}
	if err := Charge(root, "task", DefaultBudgetBytes); err != nil {
		t.Fatal(err)
	}
	after, _ = Remaining(root, "task")
	if after != 0 {
		t.Fatal("overdraft hidden")
	}
	if _, err := Execute(root, Request{Action: "checkpoint", TaskID: "task", Note: "formal result was delivered"}); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("expected explicit exhaustion: %v", err)
	}
	s, err := load(root, "task")
	if err != nil {
		t.Fatal(err)
	}
	if s.Note != "formal result was delivered" {
		t.Fatal("exhausted phase lost checkpoint")
	}
	r := call(t, root, Request{Action: "resume", EventID: "host-2"})
	if r.State.UsedBytes >= DefaultBudgetBytes {
		t.Fatal("new Host event did not reset budget")
	}
}

func TestSearchAndPendingContinueWithoutSilentTruncation(t *testing.T) {
	root := t.TempDir()
	entries := []string{}
	for i := 0; i < 150; i++ {
		entries = append(entries, fmt.Sprintf("file%03d.go[CD9S]: F:matching entry | R:- | A:- | S:-", i))
	}
	write(t, root, "aoci.txt", legacy(root, entries...))
	start(t, root)
	cursor := ""
	seen := map[string]bool{}
	for i := 0; i < 30; i++ {
		r := call(t, root, Request{Action: "search", Query: "matching", Cursor: cursor, LimitBytes: 2048})
		for _, item := range r.Items {
			if seen[item.Ref] {
				t.Fatal("duplicate search page")
			}
			seen[item.Ref] = true
		}
		cursor = r.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 150 || cursor != "" {
		t.Fatalf("search truncated: %d", len(seen))
	}
	pending := []string{}
	for i := 0; i < 100; i++ {
		pending = append(pending, fmt.Sprintf("unresolved-%03d", i))
	}
	call(t, root, Request{Action: "checkpoint", Pending: pending})
	cursor = ""
	n := 0
	for i := 0; i < 30; i++ {
		r := call(t, root, Request{Action: "status", Kind: "pending", Cursor: cursor, LimitBytes: 2048})
		n += len(r.Items)
		cursor = r.NextCursor
		if cursor == "" {
			break
		}
	}
	if n != 100 {
		t.Fatalf("pending truncated: %d", n)
	}
	call(t, root, Request{Action: "checkpoint", Pending: []string{"additional"}})
	r := call(t, root, Request{Action: "status"})
	if r.State.PendingCount != 101 {
		t.Fatal("checkpoint cleared prior pending queue")
	}
}

func TestCoveredRangesAndIndivisibleCapacity(t *testing.T) {
	s := &state{Evidence: []evidence{{EvidenceRef: EvidenceRef{Kind: "source", Ref: "a", SHA256: "x", End: 10}, Valid: true}, {EvidenceRef: EvidenceRef{Kind: "source", Ref: "a", SHA256: "x", Start: 10, End: 20}, Valid: true}}}
	if !s.hasEvidence(EvidenceRef{Kind: "source", Ref: "a", SHA256: "x", Start: 5, End: 15}) {
		t.Fatal("combined ranges not recognized")
	}
	if err := s.addEvidence(EvidenceRef{Kind: "source", Ref: "a", SHA256: "x", Start: 5, End: 15}); err != nil || s.Progress != 0 {
		t.Fatal("overlapping reread manufactured progress")
	}
	root := t.TempDir()
	write(t, root, "aoci.txt", legacy(root, "a.go[CD9S]: F:"+strings.Repeat("long", 5000)+" | R:- | A:- | S:-"))
	start(t, root)
	search := call(t, root, Request{Action: "search", Query: "long"})
	r := call(t, root, Request{Action: "read", Kind: "entry", Refs: []string{search.Items[0].Ref}})
	if r.Status != "capacity_stop" || r.NextCursor == "" || r.Remaining != 1 || len(r.Items) != 0 || r.State.Progress != 0 {
		t.Fatalf("indivisible entry silently truncated: %#v", r)
	}
}

func TestSharedMapReuseStaleEvidenceAndVersionConflict(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "source evidence")
	start(t, root)
	call(t, root, Request{Action: "start", TaskID: "old", Goal: "an older concurrent task"})
	first := call(t, root, Request{Action: "read", Kind: "source", Path: "a.go"})
	call(t, root, Request{Action: "map", MapText: "This module is represented by a.go; its behavior is still unknown.", MapEvidence: []EvidenceRef{*first.Items[0].Evidence}})
	reused := call(t, root, Request{Action: "start", TaskID: "next", Goal: "a different task"})
	if reused.Status != "map_available" || !reused.State.MapValid || reused.State.MapVersion != 1 || reused.State.EvidenceCount != 0 {
		t.Fatalf("shared map not reused honestly: %#v", reused)
	}
	loaded := call(t, root, Request{Action: "read", TaskID: "next", Kind: "map"})
	if loaded.Items[0].Text == "" || loaded.State.Progress != 0 {
		t.Fatal("map read fabricated source progress")
	}
	oldEvidence := call(t, root, Request{Action: "read", TaskID: "old", Kind: "source", Path: "a.go"})
	if _, err := Execute(root, Request{Action: "map", TaskID: "old", MapText: "an old task must not overwrite a newer map", MapEvidence: []EvidenceRef{*oldEvidence.Items[0].Evidence}}); err == nil || !strings.Contains(err.Error(), "version_conflict") {
		t.Fatalf("stale task overwrote map: %v", err)
	}
	refreshed := call(t, root, Request{Action: "read", TaskID: "old", Kind: "map"})
	if refreshed.State.Phase != oldEvidence.State.Phase || refreshed.State.UsedBytes <= oldEvidence.State.UsedBytes {
		t.Fatal("map refresh reset the phase budget")
	}
	updated := call(t, root, Request{Action: "map", TaskID: "old", MapText: "A new revision based on the same inspected evidence.", MapEvidence: []EvidenceRef{*oldEvidence.Items[0].Evidence}})
	if updated.State.MapVersion != 2 {
		t.Fatal("shared map version did not advance")
	}
	write(t, root, "a.go", "source has drifted")
	stale := call(t, root, Request{Action: "start", TaskID: "stale", Goal: "continue after drift"})
	if stale.Status != "map_stale" || stale.State.MapValid {
		t.Fatalf("stale map was trusted: %#v", stale)
	}
	staleRead := call(t, root, Request{Action: "read", TaskID: "stale", Kind: "map"})
	if staleRead.Status != "map_stale" || len(staleRead.Items) == 0 {
		t.Fatal("stale map not retained for repair")
	}
	for _, id := range []string{"system-map", "System-Map"} {
		if _, err := Execute(root, Request{Action: "start", TaskID: id, Goal: "must not collide"}); err == nil {
			t.Fatal("task ID can overwrite shared map asset")
		}
	}
}

func TestSavedOutputIsTaskScopedAndOnlyDeliveredRangesAreProgress(t *testing.T) {
	root := t.TempDir()
	start(t, root)
	text := strings.Repeat(`{"candidate":"实际证据","id":"abc"}`, 600)
	ref, err := SaveOutput(root, "task", text)
	if err != nil {
		t.Fatal(err)
	}
	before := call(t, root, Request{Action: "status"})
	if before.State.Progress != 0 {
		t.Fatal("saving output claimed Host had read it")
	}
	read := call(t, root, Request{Action: "read", Kind: "result", Path: ref, LimitBytes: 2048})
	if read.NextCursor == "" || read.State.Progress != 1 {
		t.Fatal("result not paged or not recorded")
	}
	again := call(t, root, Request{Action: "read", Kind: "result", Path: ref, LimitBytes: 2048})
	if again.State.Progress != 1 {
		t.Fatal("same result range manufactured progress")
	}
	call(t, root, Request{Action: "start", TaskID: "other", Goal: "different task"})
	if _, err = Execute(root, Request{Action: "read", TaskID: "other", Kind: "result", Path: ref}); err == nil {
		t.Fatal("cross-task result read accepted")
	}
	if _, err = Execute(root, Request{Action: "read", TaskID: "task", Kind: "result", Path: "../task.json"}); err == nil {
		t.Fatal("output ref accepted traversal")
	}
	call(t, root, Request{Action: "resume", EventID: "host-2"})
	next := call(t, root, Request{Action: "read", Kind: "result", Path: ref, Cursor: read.NextCursor, LimitBytes: 2048})
	if next.Items[0].Evidence.Start != read.Items[0].Evidence.End || next.State.Progress != 2 {
		t.Fatal("result did not continue after reconnect")
	}
	write(t, root, ".aoci/task-context/outputs/task/"+ref+".txt", "tampered")
	status := call(t, root, Request{Action: "status"})
	if status.State.InvalidEvidenceCount != 2 {
		t.Fatal("tampered retained output remained valid")
	}
}

func TestAppliedHookIsPostimageBoundAndIdempotent(t *testing.T) {
	root := t.TempDir()
	line := "a.go[CD9S]: F:before | R:- | A:- | S:-"
	write(t, root, "aoci.txt", legacy(root, line))
	start(t, root)
	match := call(t, root, Request{Action: "search", Query: "before"})
	call(t, root, Request{Action: "read", Kind: "entry", Refs: []string{match.Items[0].Ref}})
	call(t, root, Request{Action: "checkpoint", Pending: []string{"cross-batch relation remains unknown"}})
	write(t, root, "aoci.txt", legacy(root, strings.Replace(line, "before", "after", 1)))
	env, err := loadEnvironment(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = RecordApplied(root, "task", "batch-1", "wrong"); err == nil {
		t.Fatal("accepted wrong Apply postimage")
	}
	if err = RecordApplied(root, "task", "", env.identity); err == nil {
		t.Fatal("accepted unbound batch")
	}
	if err = RecordApplied(root, "task", "batch-1", env.identity); err != nil {
		t.Fatal(err)
	}
	if err = RecordApplied(root, "task", "batch-1", env.identity); err != nil {
		t.Fatal(err)
	}
	s, err := load(root, "task")
	if err != nil {
		t.Fatal(err)
	}
	if s.Progress != 2 || len(s.CompletedBatches) != 1 || s.LastBatchID != "batch-1" || len(s.Pending) != 2 || s.Evidence[0].Valid {
		t.Fatalf("wrong machine progress accounting: %#v", s)
	}
	r := call(t, root, Request{Action: "resume", EventID: "host-2"})
	if r.State.CompletedBatchCount != 1 || r.State.LastBatchID != "batch-1" {
		t.Fatal("machine batch progress lost on resume")
	}
	if _, err = Execute(root, Request{Action: "applied", TaskID: "task", Note: "done"}); err == nil {
		t.Fatal("formal progress hook exposed as Host action")
	}
}

func TestConfigurationAndManagedObserveBoundaries(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "a")
	write(t, root, "excluded.go", "excluded")
	start(t, root)
	read := call(t, root, Request{Action: "read", Kind: "source", Path: "a.go"})
	call(t, root, Request{Action: "map", MapText: "a.go is the inspected source.", MapEvidence: []EvidenceRef{*read.Items[0].Evidence}})
	write(t, root, ".aoci/config.json", `{"exclude_files":["excluded.go"]}`)
	r := call(t, root, Request{Action: "status"})
	if r.State.EvidenceCount != 0 || r.State.MapValid {
		t.Fatal("configuration drift left evidence reliable")
	}
	if _, err := Execute(root, Request{Action: "read", TaskID: "task", Kind: "source", Path: "excluded.go"}); err == nil {
		t.Fatal("configured excluded file was opened")
	}
	// A policy role may expose an observe source while an exclude rule still
	// bars its neighboring file. No formal index is needed for either decision.
	cfg := config.DefaultConfig()
	policy := managedscope.DefaultPolicy(machinecontract.ScopeProfileCustom)
	policy.Rules = []managedscope.Rule{
		{RuleID: "observe", Action: machinecontract.ScopeRoleObserve, Pattern: "a.go", PatternKind: machinecontract.ScopePatternGlob, Reason: "context", Source: machinecontract.ScopeRuleUser, CreatedBy: "user", Order: 0, Enabled: true},
		{RuleID: "exclude", Action: machinecontract.ScopeRoleExclude, Pattern: "excluded.go", PatternKind: machinecontract.ScopePatternGlob, Reason: "excluded", Source: machinecontract.ScopeRuleUser, CreatedBy: "user", Order: 1, Enabled: true},
	}
	cfg.ManagedScope = &policy
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, ".aoci/config.json", string(b))
	r = call(t, root, Request{Action: "read", Kind: "source", Path: "a.go"})
	if r.Items[0].Text != "a" {
		t.Fatal("observe role source was not available")
	}
	if _, err = Execute(root, Request{Action: "read", TaskID: "task", Kind: "source", Path: "excluded.go"}); err == nil {
		t.Fatal("managed exclude role source was opened")
	}
}

func TestUnindexedSourceSearchCursorBindsExactSelection(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 90; i++ {
		write(t, root, fmt.Sprintf("file%03d.go", i), "source")
	}
	start(t, root)
	first := call(t, root, Request{Action: "search", Kind: "source", Query: "file", LimitBytes: 2048})
	if first.NextCursor == "" {
		t.Fatal("fixture did not page")
	}
	if first.State.IndexIdentity != "" {
		t.Fatal("fixture unexpectedly indexed")
	}
	req := Request{Action: "search", TaskID: "task", Kind: "source", Query: "file", LimitBytes: 2048, Cursor: first.NextCursor}
	write(t, root, "file000-extra.go", "new source")
	if _, err := Execute(root, req); err == nil || !strings.Contains(err.Error(), "cursor_stale") {
		t.Fatalf("source addition reused old offset: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "file000-extra.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "file000.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(root, req); err == nil || !strings.Contains(err.Error(), "cursor_stale") {
		t.Fatalf("source deletion reused old offset: %v", err)
	}
}

func TestRecoveryControlPreservesCheckpointWithoutReadingEvidence(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "inspected source")
	start(t, root)
	read := call(t, root, Request{Action: "read", Kind: "source", Path: "a.go"})
	call(t, root, Request{Action: "map", MapText: "a.go provides the inspected source; semantics remain to investigate.", MapEvidence: []EvidenceRef{*read.Items[0].Evidence}})
	call(t, root, Request{Action: "checkpoint", Pending: []string{"an unresolved relationship"}})
	// A malformed formal index makes every normal material-capable call fail.
	// Recovery controls must not consult it or re-open the now missing source.
	write(t, root, "aoci.txt", "#AOCI-ROOT-MANIFEST: broken\n")
	if err := os.Remove(filepath.Join(root, "a.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(root, Request{Action: "status", TaskID: "task"}); err == nil {
		t.Fatal("fixture did not block normal cognition load")
	}
	r, err := ExecuteControl(root, Request{Action: "checkpoint", TaskID: "task", Note: "preserve this during Recovery", Pending: []string{"Recovery must finish"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Verification != "cached_unverified" || r.State.MapValid || r.State.PendingCount != 2 || r.State.EvidenceCount != 1 {
		t.Fatalf("cached state falsely revalidated: %#v", r)
	}
	saved, err := load(root, "task")
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Map.Valid || !saved.Evidence[0].Valid {
		t.Fatal("control silently reconciled evidence without reading it")
	}
	checkpoint, err := ExecuteControl(root, Request{Action: "status", TaskID: "task", Kind: "checkpoint"})
	if err != nil || checkpoint.Items[0].Text != "preserve this during Recovery" {
		t.Fatalf("control lost note: %v", err)
	}
	for _, req := range []Request{{Action: "resume", EventID: "invented"}, {Action: "read", Kind: "map"}, {Action: "status", Kind: "evidence"}} {
		req.TaskID = "task"
		if _, err = ExecuteControl(root, req); err == nil {
			t.Fatal("Recovery control exposed material or reset budget")
		}
	}
	after, err := ExecuteControl(root, Request{Action: "charge", TaskID: "task", ExternalBytes: 123})
	if err != nil {
		t.Fatal(err)
	}
	if after.State.Phase != r.State.Phase || after.State.UsedBytes <= checkpoint.State.UsedBytes+123 {
		t.Fatal("control charge reset phase or missed output cost")
	}
}

func TestResolutionClosesOnlyNamedPendingWithoutMachineProgress(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "actual inspected implementation")
	start(t, root)
	call(t, root, Request{Action: "checkpoint", Pending: []string{"check the caller", "check the configuration"}})
	read := call(t, root, Request{Action: "read", Kind: "source", Path: "a.go"})
	ev := *read.Items[0].Evidence
	resolved := call(t, root, Request{Action: "checkpoint", Note: "The inspected implementation supports this caller judgment; configuration remains unchecked.", Resolved: []string{"check the caller"}, ResolutionEvidence: []EvidenceRef{ev}, Pending: []string{"review a test"}})
	if resolved.State.PendingCount != 2 || resolved.State.DecisionCount != 1 || resolved.State.Progress != read.State.Progress || resolved.State.CompletedBatchCount != 0 {
		t.Fatalf("resolution fabricated progress or dropped pending: %#v", resolved.State)
	}
	pending := call(t, root, Request{Action: "status", Kind: "pending"})
	seen := map[string]bool{}
	for _, item := range pending.Items {
		seen[item.Ref] = true
	}
	if seen["check the caller"] || !seen["check the configuration"] || !seen["review a test"] {
		t.Fatal("resolution cleared unrelated pending")
	}
	decisions := call(t, root, Request{Action: "status", Kind: "decisions"})
	if len(decisions.Items) != 1 || decisions.Items[0].Decision == nil || !decisions.Items[0].Decision.Valid || decisions.Items[0].Decision.Pending[0] != "check the caller" {
		t.Fatalf("resolution judgment not reviewable: %#v", decisions)
	}
	for _, req := range []Request{
		{Action: "checkpoint", Resolved: []string{"missing"}, Note: "unknown item", ResolutionEvidence: []EvidenceRef{ev}},
		{Action: "checkpoint", Resolved: []string{"check the configuration"}, ResolutionEvidence: []EvidenceRef{ev}},
		{Action: "checkpoint", Resolved: []string{"check the configuration"}, Note: "without evidence"},
		{Action: "checkpoint", Resolved: []string{"check the configuration"}, Note: "undelivered range", ResolutionEvidence: []EvidenceRef{{Kind: ev.Kind, Ref: ev.Ref, SHA256: ev.SHA256, End: ev.End + 1}}},
	} {
		req.TaskID = "task"
		if _, err := Execute(root, req); err == nil {
			t.Fatal("unbound resolution accepted")
		}
	}
	if _, err := ExecuteControl(root, Request{Action: "checkpoint", TaskID: "task", Resolved: []string{"check the configuration"}, Note: "Recovery cannot validate this", ResolutionEvidence: []EvidenceRef{ev}}); err == nil {
		t.Fatal("Recovery control resolved unverified work")
	}
	status := call(t, root, Request{Action: "status"})
	if status.State.PendingCount != 2 || status.State.DecisionCount != 1 {
		t.Fatal("rejected resolution changed task")
	}
}

func TestResolutionRejectsOldEvidenceAndReopensJudgmentOnDrift(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "old implementation")
	start(t, root)
	call(t, root, Request{Action: "checkpoint", Pending: []string{"check the interface", "still unknown"}})
	read := call(t, root, Request{Action: "read", Kind: "source", Path: "a.go"})
	old := *read.Items[0].Evidence
	call(t, root, Request{Action: "checkpoint", Resolved: []string{"check the interface"}, Note: "The inspected interface supports the judgment.", ResolutionEvidence: []EvidenceRef{old}})
	write(t, root, "a.go", "changed interface implementation")
	if _, err := Execute(root, Request{Action: "checkpoint", TaskID: "task", Resolved: []string{"still unknown"}, Note: "Old source cannot support a new judgment.", ResolutionEvidence: []EvidenceRef{old}}); err == nil {
		t.Fatal("old hash resolved current pending")
	}
	pending := call(t, root, Request{Action: "status", Kind: "pending"})
	seen := map[string]bool{}
	for _, item := range pending.Items {
		seen[item.Ref] = true
	}
	if !seen["check the interface"] || !seen["still unknown"] {
		t.Fatal("evidence drift failed to reopen related judgment")
	}
	decisions := call(t, root, Request{Action: "status", Kind: "decisions"})
	if decisions.Items[0].Decision.Valid {
		t.Fatal("stale resolution remained valid")
	}
	reread := call(t, root, Request{Action: "read", Kind: "source", Path: "a.go"})
	fixed := call(t, root, Request{Action: "checkpoint", Resolved: []string{"check the interface"}, Note: "Rechecked against the changed interface.", ResolutionEvidence: []EvidenceRef{*reread.Items[0].Evidence}})
	if fixed.State.DecisionCount != 2 || fixed.State.Progress != reread.State.Progress {
		t.Fatal("renewed judgment lost history or fabricated progress")
	}
}

func TestResolutionHistoryPagesAndOversizedJudgmentFailsExplicitly(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "source")
	start(t, root)
	read := call(t, root, Request{Action: "read", Kind: "source", Path: "a.go"})
	ev := *read.Items[0].Evidence
	pending := []string{}
	for i := 0; i < 12; i++ {
		pending = append(pending, fmt.Sprintf("review-%02d", i))
	}
	call(t, root, Request{Action: "checkpoint", Pending: pending})
	for _, p := range pending {
		call(t, root, Request{Action: "checkpoint", Resolved: []string{p}, Note: strings.Repeat("bounded explanation ", 20), ResolutionEvidence: []EvidenceRef{ev}})
	}
	n := 0
	cursor := ""
	for i := 0; i < 20; i++ {
		r := call(t, root, Request{Action: "status", Kind: "decisions", Cursor: cursor, LimitBytes: 2048})
		b, _ := json.Marshal(r)
		if len(b) > 2048 {
			t.Fatal("decision page exceeded bound")
		}
		n += len(r.Items)
		cursor = r.NextCursor
		if cursor == "" {
			break
		}
	}
	if n != 12 {
		t.Fatalf("decision history truncated: %d", n)
	}
	large := []string{}
	for i := 0; i < 10; i++ {
		large = append(large, fmt.Sprintf("%02d-", i)+strings.Repeat("x", 500))
	}
	call(t, root, Request{Action: "checkpoint", Pending: large})
	_, err := Execute(root, Request{Action: "checkpoint", TaskID: "task", Resolved: large, Note: "A judgment must remain independently deliverable.", ResolutionEvidence: []EvidenceRef{ev}})
	if err == nil || !strings.Contains(err.Error(), "record_capacity") {
		t.Fatalf("oversized decision was silently truncated: %v", err)
	}
	r := call(t, root, Request{Action: "status"})
	if r.State.PendingCount != 10 || r.State.DecisionCount != 12 {
		t.Fatal("failed oversized judgment lost pending")
	}
}
