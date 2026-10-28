package taskcontext

import (
	"errors"
	"path/filepath"
	"strings"

	afs "github.com/aoci-spec/aoci-code/internal/fs"
)

// RecordApplied is an internal success-handler hook, never a Host action.
// The caller must have verified a successful formal Code Apply and its machine
// batch ID. This function confirms the current postimage and records each
// batch once; it neither performs Apply nor creates a formal Apply receipt.
func RecordApplied(root, taskID, batchID, postIndexIdentity string) error {
	if !validTaskID(taskID) || strings.TrimSpace(batchID) == "" || len(batchID) > 128 || postIndexIdentity == "" {
		return errors.New("task_applied_binding_invalid")
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
	env, err := loadEnvironment(root)
	if err != nil {
		return err
	}
	if env.identity != postIndexIdentity {
		return errors.New("task_applied_postimage_changed")
	}
	if err = s.reconcile(env); err != nil {
		return err
	}
	for _, id := range s.CompletedBatches {
		if id == batchID {
			return save(root, s)
		}
	}
	if len(s.CompletedBatches) >= maxRecords {
		return errors.New("task_applied_capacity")
	}
	s.CompletedBatches = append(s.CompletedBatches, batchID)
	s.LastBatchID = batchID
	s.Progress++
	return save(root, s)
}
