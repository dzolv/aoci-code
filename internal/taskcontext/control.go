package taskcontext

import (
	"errors"
	"math"
	"path/filepath"
	"unicode/utf8"

	afs "github.com/aoci-spec/aoci-code/internal/fs"
)

// ExecuteControl preserves task notes while the caller's formal Recovery
// guard blocks all new material. It never loads configuration, cognition,
// sources, or a system map and makes no claim that cached evidence is current.
func ExecuteControl(root string, req Request) (*Response, error) {
	if !validTaskID(req.TaskID) {
		return nil, errors.New("task_id_invalid")
	}
	if len(req.Resolved) > 0 || len(req.ResolutionEvidence) > 0 {
		return nil, errors.New("task_recovery_resolution_blocked")
	}
	if req.Action != "checkpoint" && req.Action != "status" && req.Action != "charge" {
		return nil, errors.New("task_recovery_control_only")
	}
	if req.Action == "status" && req.Kind != "" && req.Kind != "pending" && req.Kind != "checkpoint" {
		return nil, errors.New("task_recovery_material_blocked")
	}
	if len(req.Note) > 2048 || !utf8.ValidString(req.Note) || len(req.Pending) > 256 || req.ExternalBytes < 0 || req.LimitBytes < 0 || len(req.Cursor) > 2048 {
		return nil, errors.New("task_request_capacity")
	}
	if req.LimitBytes > 0 && req.LimitBytes < 1024 {
		return nil, errors.New("task_page_too_small")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err = regularPath(root, statePath(req.TaskID), false); err != nil {
		return nil, err
	}
	lock, err := afs.AcquireIndexLock(root)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	s, err := load(root, req.TaskID)
	if err != nil {
		return nil, err
	}
	var r *Response
	switch req.Action {
	case "status":
		pageReq := req
		if req.Kind != "" {
			pageReq.LimitBytes = max(1, pageLimit(s, req)-256)
		}
		r, err = statusPage(s, pageReq)
		if err != nil {
			return nil, err
		}
	case "checkpoint":
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
	case "charge":
		if req.ExternalBytes > math.MaxInt-s.Used {
			return nil, errors.New("task_charge_overflow")
		}
		s.Used += req.ExternalBytes
		r = s.response("charged")
	}
	r.Verification = "cached_unverified"
	r.Next = "formal Recovery blocks material; cached evidence and map have not been revalidated"
	if err = meter(s, r, true); err != nil {
		if req.Action == "checkpoint" || req.Action == "charge" {
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
