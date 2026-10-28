package taskcontext

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	afs "github.com/aoci-spec/aoci-code/internal/fs"
)

var ErrBudgetExhausted = errors.New("task_budget_exhausted: resume with a new Host event after preserving progress")

// Execute never changes formal cognition, Baseline, batches, or receipts.
func Execute(root string, req Request) (*Response, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root = filepath.Clean(root)
	if !utf8.ValidString(req.Goal + req.Note + req.MapText) {
		return nil, errors.New("task_text_utf8_invalid")
	}
	if len(req.Goal) > 1024 || len(req.Note) > 2048 || len(req.EventID) > 128 || len(req.Pending) > 256 || len(req.Resolved) > 256 || len(req.ResolutionEvidence) > 64 || len(req.Refs) > 256 || len(req.Query) > 1024 || len(req.Path) > 1024 || len(req.Cursor) > 2048 {
		return nil, errors.New("task_request_capacity")
	}
	if req.Action != "checkpoint" && (len(req.Resolved) > 0 || len(req.ResolutionEvidence) > 0) {
		return nil, errors.New("task_resolution_checkpoint_only")
	}
	if req.LimitBytes < 0 || req.BudgetBytes < 0 || req.ExternalBytes < 0 {
		return nil, errors.New("task_negative_bytes")
	}
	if req.LimitBytes > 0 && req.LimitBytes < 1024 {
		return nil, errors.New("task_page_too_small")
	}
	if req.Action == "start" && req.TaskID == "" {
		var b [12]byte
		if _, err = rand.Read(b[:]); err != nil {
			return nil, err
		}
		req.TaskID = hex.EncodeToString(b[:])
	}
	if !validTaskID(req.TaskID) {
		return nil, errors.New("task_id_invalid")
	}
	// Validate the task directory boundary before the shared lock can create it.
	if _, err = regularPath(root, statePath(req.TaskID), true); err != nil {
		return nil, err
	}
	lock, err := afs.AcquireIndexLock(root)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	env, err := loadEnvironment(root)
	if err != nil {
		return nil, err
	}
	s, err := load(root, req.TaskID)
	if req.Action == "start" {
		if err == nil {
			return nil, errors.New("task_already_exists: use resume")
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		budget := req.BudgetBytes
		if budget == 0 {
			budget = DefaultBudgetBytes
		}
		if budget < 2*ReserveBytes || budget > 1024*1024 {
			return nil, errors.New("task_budget_invalid")
		}
		if strings.TrimSpace(req.Goal) == "" {
			return nil, errors.New("task_goal_required")
		}
		s = &state{Version: 1, Root: root, TaskID: req.TaskID, Goal: req.Goal, ConfigHash: env.cfgHash, IndexIdentity: env.identity, Phase: 1, Budget: budget, Page: DefaultPageBytes}
		err = nil
		s.Map, _, err = loadSharedMap(root)
		if err != nil {
			return nil, err
		}
		if req.EventID != "" {
			s.Events = []string{req.EventID}
		}
	} else if err != nil {
		return nil, err
	}
	if err = s.reconcile(env); err != nil {
		return nil, err
	}
	var r *Response
	switch req.Action {
	case "start":
		r = s.response("map_missing")
		if s.Map.Valid {
			r.Status = "map_available"
			r.Next = "read kind=map to load the validated shared Host-authored map"
		} else if s.Map.Version > 0 {
			r.Status = "map_stale"
		}
	case "resume":
		if strings.TrimSpace(req.EventID) == "" {
			return nil, errors.New("task_resume_new_host_event_required")
		}
		seen := false
		for _, id := range s.Events {
			if id == req.EventID {
				seen = true
			}
		}
		if !seen {
			if len(s.Events) >= 256 {
				return nil, errors.New("task_event_capacity")
			}
			if s.Progress == s.PhaseProgress {
				s.Stalls++
				s.Page = max(2048, s.Page/2)
			} else {
				s.Stalls = 0
			}
			s.PhaseProgress = s.Progress
			s.Phase++
			s.Used = 0
			s.Events = append(s.Events, req.EventID)
			s.Map, _, err = loadSharedMap(root)
			if err != nil {
				return nil, err
			}
			if err = s.reconcile(env); err != nil {
				return nil, err
			}
		}
		status := "resumed"
		if seen {
			status = "resume_event_replayed"
		}
		if s.Stalls >= 3 {
			status = "no_progress_stop"
		}
		r = s.response(status)
	case "checkpoint":
		if err = s.resolvePending(env, req); err != nil {
			return nil, err
		}
		if len(s.Pending)+len(req.Pending) > maxRecords {
			return nil, errors.New("task_pending_capacity")
		}
		for _, p := range req.Pending {
			if len(p) > 512 || !utf8.ValidString(p) {
				return nil, errors.New("task_pending_invalid")
			}
			if err = s.addPending(p); err != nil {
				return nil, err
			}
		}
		s.Note = req.Note
		r = s.response("checkpoint_saved")
	case "status":
		r, err = statusPage(s, req)
	case "charge":
		if req.ExternalBytes > math.MaxInt-s.Used {
			return nil, errors.New("task_charge_overflow")
		}
		s.Used += req.ExternalBytes
		r = s.response("charged")
	case "map":
		if len(req.MapText) == 0 || len(req.MapText) > DefaultPageBytes || len(req.MapEvidence) == 0 || len(req.MapEvidence) > 64 {
			return nil, errors.New("task_map_capacity_or_evidence_missing")
		}
		for _, ref := range req.MapEvidence {
			if ref.Kind != "source" && ref.Kind != "entry" && ref.Kind != "meta" {
				return nil, errors.New("task_map_requires_repository_evidence")
			}
			if !s.hasEvidence(ref) {
				return nil, errors.New("task_map_evidence_not_delivered")
			}
			b, e := env.current(ref)
			if ref.Kind == "result" {
				b, e = readOutput(root, s.TaskID, ref.Ref)
			}
			if e != nil || digest(b) != ref.SHA256 {
				return nil, errors.New("task_map_evidence_changed")
			}
		}
		nextMap := taskMap{Text: req.MapText, Version: s.Map.Version + 1, Evidence: req.MapEvidence, Valid: true, ConfigHash: env.cfgHash, RepositoryRoot: root, AuthorTaskID: s.TaskID}
		if err = saveSharedMap(root, s.Map.Version, nextMap); err != nil {
			return nil, err
		}
		s.Map = nextMap
		r = s.response("map_saved")
	case "read", "search", "relations":
		if s.Stalls >= 3 {
			r = s.response("no_progress_stop")
			r.Next = "new effective evidence or a smaller task is required; checkpoints and prose do not prove progress"
			break
		}
		r, err = deliver(s, env, req)
	default:
		return nil, fmt.Errorf("task_action_invalid: %s", req.Action)
	}
	if err != nil {
		return nil, err
	}
	control := req.Action == "checkpoint" || req.Action == "status" || req.Action == "resume" || req.Action == "charge" || req.Action == "map"
	if err = meter(s, r, control); err != nil {
		// Explicit external charges and checkpoints are durable even when an
		// already overdrawn phase has no capacity left for their response.
		if req.Action == "charge" || req.Action == "checkpoint" {
			if saveErr := save(root, s); saveErr != nil {
				return nil, saveErr
			}
		}
		return nil, err
	}
	if err = save(root, s); err != nil {
		return nil, err
	}
	return r, nil
}

// Remaining is a cheap preflight for other same-server read handlers. The
// reserve belongs to checkpoint/control responses, not further material reads.
func Remaining(root, taskID string) (int, error) {
	if !validTaskID(taskID) {
		return 0, errors.New("task_id_invalid")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return 0, err
	}
	if _, err = regularPath(root, statePath(taskID), false); err != nil {
		return 0, err
	}
	lock, err := afs.AcquireIndexLock(root)
	if err != nil {
		return 0, err
	}
	defer lock.Release()
	s, err := load(root, taskID)
	if err != nil {
		return 0, err
	}
	return max(0, s.Budget-ReserveBytes-s.Used), nil
}

// Charge records already delivered same-server or explicitly reported external
// bytes even when they exceed the budget. It must never suppress a formal
// write result after that write has run.
func Charge(root, taskID string, n int) error {
	if !validTaskID(taskID) || n < 0 {
		return errors.New("task_charge_invalid")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if _, err = regularPath(root, statePath(taskID), false); err != nil {
		return err
	}
	lock, err := afs.AcquireIndexLock(root)
	if err != nil {
		return err
	}
	defer lock.Release()
	s, err := load(root, taskID)
	if err != nil {
		return err
	}
	if n > math.MaxInt-s.Used {
		return errors.New("task_charge_overflow")
	}
	s.Used += n
	return save(root, s)
}

func meter(s *state, r *Response, control bool) error {
	maxBytes := s.Budget
	if !control {
		maxBytes -= ReserveBytes
	}
	before := s.Used
	// The response's own decimal counter is included in its final byte count.
	for i := 0; i < 8; i++ {
		r.State = s.summary()
		if r.Verification == "cached_unverified" {
			r.State.MapValid = false
		}
		b, _ := json.Marshal(r)
		if len(b) > math.MaxInt-before {
			s.Used = before
			return ErrBudgetExhausted
		}
		next := before + len(b)
		if next == s.Used {
			break
		}
		s.Used = next
	}
	if s.Used <= maxBytes {
		return nil
	}
	// Preserve useful checkpoint space and do not persist any evidence from an
	// undelivered body. deliver preflights material before mutating evidence.
	s.Used = before
	if len(r.Items) > 0 {
		return ErrBudgetExhausted
	}
	if control {
		return ErrBudgetExhausted
	}
	r.Status = "budget_stop"
	r.Next = "checkpoint, then resume with a new Host event"
	return meter(s, r, true)
}
