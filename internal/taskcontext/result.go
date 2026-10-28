package taskcontext

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"unicode/utf8"

	afs "github.com/aoci-spec/aoci-code/internal/fs"
)

var outputIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// SaveOutput retains a complete tool result before bounded delivery. Saving
// is not evidence that the Host has read it and never counts as progress.
func SaveOutput(root, taskID, text string) (string, error) {
	if !validTaskID(taskID) {
		return "", errors.New("task_id_invalid")
	}
	if len(text) > 32*1024*1024 {
		return "", errors.New("task_output_capacity_exceeded")
	}
	if !utf8.ValidString(text) {
		return "", errors.New("task_output_utf8_invalid")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if _, err := regularPath(root, statePath(taskID), false); err != nil {
		return "", err
	}
	lock, err := afs.AcquireIndexLock(root)
	if err != nil {
		return "", err
	}
	defer lock.Release()
	if _, err := load(root, taskID); err != nil {
		return "", err
	}
	ref := digest([]byte(text))
	rel := ".aoci/task-context/outputs/" + taskID + "/" + ref + ".txt"
	path, err := regularPath(root, rel, true)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	if _, err := regularPath(root, rel, true); err != nil {
		return "", err
	}
	if err := afs.AtomicWrite(path, []byte(text)); err != nil {
		return "", err
	}
	return ref, nil
}

func readOutput(root, taskID, ref string) ([]byte, error) {
	if !validTaskID(taskID) || !outputIDPattern.MatchString(ref) {
		return nil, errors.New("task_output_ref_invalid")
	}
	data, err := readRegular(root, ".aoci/task-context/outputs/"+taskID+"/"+ref+".txt")
	if err != nil {
		return nil, err
	}
	if digest(data) != ref {
		return nil, errors.New("task_output_changed")
	}
	return data, nil
}
