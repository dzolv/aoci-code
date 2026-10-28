package taskcontext

import (
	"encoding/json"
	"errors"
	"strings"
)

func (s *state) resolvePending(env *environment, req Request) error {
	if len(req.Resolved) == 0 {
		if len(req.ResolutionEvidence) > 0 {
			return errors.New("task_resolution_items_required")
		}
		return nil
	}
	if strings.TrimSpace(req.Note) == "" || len(req.ResolutionEvidence) == 0 {
		return errors.New("task_resolution_note_and_evidence_required")
	}
	if len(s.Resolutions) >= maxRecords {
		return errors.New("task_resolution_capacity")
	}
	existing := map[string]bool{}
	for _, pending := range s.Pending {
		existing[pending] = true
	}
	selected := map[string]bool{}
	for _, pending := range req.Resolved {
		if !existing[pending] {
			return errors.New("task_resolution_pending_unknown")
		}
		if selected[pending] {
			return errors.New("task_resolution_duplicate")
		}
		selected[pending] = true
	}
	for _, pending := range req.Pending {
		if selected[pending] {
			return errors.New("task_resolution_pending_conflict")
		}
	}
	for _, ref := range req.ResolutionEvidence {
		if ref.Kind != "source" && ref.Kind != "entry" && ref.Kind != "meta" {
			return errors.New("task_resolution_repository_evidence_required")
		}
		if !s.hasEvidence(ref) {
			return errors.New("task_resolution_evidence_not_delivered_or_stale")
		}
		data, err := env.current(ref)
		if err != nil || digest(data) != ref.SHA256 {
			return errors.New("task_resolution_evidence_changed")
		}
		if ref.End > len(data) || (ref.Start == ref.End && len(data) > 0) {
			return errors.New("task_resolution_evidence_range_invalid")
		}
	}
	decision := Resolution{Pending: append([]string{}, req.Resolved...), Note: req.Note, Evidence: append([]EvidenceRef{}, req.ResolutionEvidence...), Valid: true}
	encoded, _ := json.Marshal(decision)
	if len(encoded) > DefaultPageBytes/2 {
		return errors.New("task_resolution_record_capacity: use fewer pending items or evidence references")
	}
	remaining := make([]string, 0, len(s.Pending)-len(selected))
	for _, pending := range s.Pending {
		if !selected[pending] {
			remaining = append(remaining, pending)
		}
	}
	s.Pending = remaining
	s.Resolutions = append(s.Resolutions, decision)
	// A Host judgment does not add machine progress or formal Apply receipts.
	return nil
}
