package taskcontext

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	afs "github.com/aoci-spec/aoci-code/internal/fs"
)

const sharedMapPath = ".aoci/task-context/system-map.json"

func loadSharedMap(root string) (taskMap, string, error) {
	b, err := readRegular(root, sharedMapPath)
	if errors.Is(err, os.ErrNotExist) {
		return taskMap{}, "", nil
	}
	if err != nil {
		return taskMap{}, "", err
	}
	var m taskMap
	if err = json.Unmarshal(b, &m); err != nil {
		return taskMap{}, "", err
	}
	if m.Version < 1 || len(m.Text) == 0 || len(m.Text) > DefaultPageBytes || len(m.Evidence) == 0 || len(m.Evidence) > 64 || m.ConfigHash == "" || m.RepositoryRoot != root || !validTaskID(m.AuthorTaskID) {
		return taskMap{}, "", errors.New("task_shared_map_invalid")
	}
	return m, digest(b), nil
}

// The task's previously loaded version is the expected version. A task which
// has not observed a newer map cannot overwrite it. The shared index lock and
// filesystem CAS both protect this independent Host-authored task asset.
func saveSharedMap(root string, expectedVersion int, m taskMap) error {
	current, preimage, err := loadSharedMap(root)
	if err != nil {
		return err
	}
	if current.Version != expectedVersion {
		return errors.New("task_map_version_conflict: read kind=map to load the current version before authoring an update")
	}
	path, err := regularPath(root, sharedMapPath, true)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if _, err = regularPath(root, sharedMapPath, true); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if preimage == "" {
		return afs.AtomicCreateCASMode(path, b, 0600)
	}
	return afs.AtomicWriteCAS(path, b, preimage)
}
