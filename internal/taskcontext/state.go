package taskcontext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/aoci-spec/aoci-code/internal/baseline"
	"github.com/aoci-spec/aoci-code/internal/cognition"
	"github.com/aoci-spec/aoci-code/internal/config"
	afs "github.com/aoci-spec/aoci-code/internal/fs"
	"github.com/aoci-spec/aoci-code/internal/managedscope"
	"github.com/aoci-spec/aoci-code/internal/managedstate"
)

const maxRecords = 4096

var taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func validTaskID(id string) bool {
	return taskIDPattern.MatchString(id) && !strings.EqualFold(id, "system-map")
}

type evidence struct {
	EvidenceRef
	Valid bool `json:"valid"`
}
type taskMap struct {
	Text           string        `json:"text"`
	Version        int           `json:"version"`
	Evidence       []EvidenceRef `json:"evidence"`
	Valid          bool          `json:"valid"`
	ConfigHash     string        `json:"config_sha256"`
	RepositoryRoot string        `json:"repository_root"`
	AuthorTaskID   string        `json:"author_task_id"`
}
type state struct {
	Version          int          `json:"version"`
	Root             string       `json:"root"`
	TaskID           string       `json:"task_id"`
	Goal             string       `json:"goal"`
	ConfigHash       string       `json:"config_sha256"`
	IndexIdentity    string       `json:"index_identity"`
	Phase            int          `json:"phase"`
	Budget           int          `json:"budget"`
	Used             int          `json:"used"`
	Page             int          `json:"page"`
	Events           []string     `json:"resume_events"`
	Progress         int          `json:"progress"`
	CompletedBatches []string     `json:"completed_batches,omitempty"`
	LastBatchID      string       `json:"last_batch_id,omitempty"`
	PhaseProgress    int          `json:"phase_progress"`
	Stalls           int          `json:"stalls"`
	Evidence         []evidence   `json:"evidence"`
	Pending          []string     `json:"pending"`
	Note             string       `json:"note,omitempty"`
	Map              taskMap      `json:"map"`
	Resolutions      []Resolution `json:"resolutions,omitempty"`
}

type environment struct {
	root     string
	cfg      *config.Config
	cfgHash  string
	set      *cognition.Set
	objects  map[string]cognition.Object
	allowed  map[string]bool
	identity string
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func jsonHash(v any) string  { b, _ := json.Marshal(v); return digest(b) }

// regularPath refuses links in every component, including the repository and
// task asset directories. It is used again immediately before reading/writing.
func regularPath(root, rel string, missing bool) (string, error) {
	clean, err := afs.NormalizeRelPath(rel)
	if err != nil {
		return "", err
	}
	if strings.Contains(clean, ":") || strings.HasSuffix(clean, "/") {
		return "", errors.New("task_path_invalid")
	}
	current := filepath.Clean(root)
	info, err := os.Lstat(current)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("task_root_unsafe")
	}
	parts := strings.Split(clean, "/")
	for i, p := range parts {
		current = filepath.Join(current, p)
		info, err = os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && missing {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return "", errors.New("task_path_unsafe")
		}
	}
	return current, nil
}

func readRegular(root, rel string) ([]byte, error) {
	path, err := regularPath(root, rel, false)
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, errors.New("task_path_changed")
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("task_path_changed")
	}
	if _, err = regularPath(root, rel, false); err != nil {
		return nil, err
	}
	return b, nil
}

func loadEnvironment(root string) (*environment, error) {
	cfg, err := config.LoadReadOnly(root)
	if err != nil {
		return nil, err
	}
	env := &environment{root: root, cfg: cfg, cfgHash: jsonHash(cfg), objects: map[string]cognition.Object{}, allowed: map[string]bool{}}
	// No high-risk opt-in opens sensitive files in task mode. The existing
	// inventory and policy evaluators remain the path-selection authority.
	opt := cfg.WalkOptions()
	opt.HighRiskOptIn = nil
	b, _, err := baseline.Load(root)
	if err != nil {
		return nil, err
	}
	exclusions, err := managedstate.CurationExclusions(root, cfg, b)
	if err != nil {
		return nil, err
	}
	if cfg.ManagedScope != nil {
		evaluation, e := managedscope.Build(root, cfg.EffectiveManagedScope(), managedscope.BuildOptions{WalkOptions: opt, CurationExclude: exclusions})
		if e != nil {
			return nil, e
		}
		for _, group := range [][]managedscope.PathEvaluation{evaluation.Index, evaluation.Observe} {
			for _, p := range group {
				env.allowed[p.Path] = true
			}
		}
	} else {
		inventory, e := afs.BuildSafeInventory(root, opt)
		if e != nil {
			return nil, e
		}
		for _, p := range inventory.ManagedCandidates {
			env.allowed[p] = !cfg.CurationExcluded(p)
		}
		for _, p := range exclusions {
			delete(env.allowed, p)
		}
	}
	// The cognition loader owns all semantic parsing. Missing cognition is a
	// normal source-first task; malformed existing cognition remains an error.
	if _, err = regularPath(root, cfg.IndexPath, false); errors.Is(err, os.ErrNotExist) {
		return env, nil
	} else if err != nil {
		return nil, err
	}
	env.set, err = cognition.Load(root, cfg.IndexPath)
	if err != nil {
		return nil, err
	}
	env.identity = env.set.CompositeIdentity
	add := func(a *cognition.Asset) {
		for _, o := range a.Objects {
			env.objects[o.CanonicalRef] = o
		}
	}
	add(&env.set.Root)
	add(&env.set.Meta)
	for _, a := range env.set.Volumes {
		add(a)
	}
	return env, nil
}

func (e *environment) current(ref EvidenceRef) ([]byte, error) {
	switch ref.Kind {
	case "source":
		if !e.allowed[ref.Ref] {
			return nil, errors.New("task_source_not_allowed")
		}
		return readRegular(e.root, ref.Ref)
	case "entry":
		o, ok := e.objects[ref.Ref]
		if !ok {
			return nil, errors.New("task_entry_missing")
		}
		return []byte(o.CanonicalLine), nil
	case "meta":
		if e.set == nil {
			return nil, errors.New("task_meta_missing")
		}
		if e.set.LayoutMode == cognition.LayoutLegacyMonolithic {
			// Legacy Raw includes entries and is deliberately not exposed as Meta.
			return nil, errors.New("task_legacy_meta_unavailable")
		}
		if e.set.Meta.State != cognition.AssetPresent {
			return nil, errors.New("task_meta_missing")
		}
		return e.set.Meta.Raw, nil
	default:
		return nil, errors.New("task_evidence_kind_invalid")
	}
}

func (s *state) reconcile(env *environment) error {
	changedConfig := s.ConfigHash != env.cfgHash
	for i := range s.Evidence {
		ev := &s.Evidence[i]
		if !ev.Valid {
			continue
		}
		b, err := env.current(ev.EvidenceRef)
		if ev.Kind == "result" {
			b, err = readOutput(env.root, s.TaskID, ev.Ref)
		}
		if err != nil || digest(b) != ev.SHA256 || changedConfig {
			ev.Valid = false
			if err := s.addPending("recheck:" + ev.Kind + ":" + ev.Ref); err != nil {
				return err
			}
		}
	}
	s.Map.Valid = len(s.Map.Evidence) > 0 && s.Map.ConfigHash == env.cfgHash && s.Map.RepositoryRoot == env.root
	if s.Map.Version > 0 && s.Map.ConfigHash != env.cfgHash {
		if err := s.addPending("map_recheck:configuration"); err != nil {
			return err
		}
	}
	for _, ref := range s.Map.Evidence {
		b, err := env.current(ref)
		if err != nil || digest(b) != ref.SHA256 || ref.Start < 0 || ref.End < ref.Start || ref.End > len(b) {
			s.Map.Valid = false
			if err = s.addPending("recheck:" + ref.Kind + ":" + ref.Ref); err != nil {
				return err
			}
		}
	}
	s.ConfigHash = env.cfgHash
	s.IndexIdentity = env.identity
	for i := range s.Resolutions {
		decision := &s.Resolutions[i]
		if !decision.Valid {
			continue
		}
		for _, ref := range decision.Evidence {
			if !s.hasEvidence(ref) {
				decision.Valid = false
				for _, pending := range decision.Pending {
					if err := s.addPending(pending); err != nil {
						return err
					}
				}
				break
			}
		}
	}
	return nil
}

func (s *state) hasEvidence(ref EvidenceRef) bool {
	if ref.Start < 0 || ref.End < ref.Start {
		return false
	}
	ranges := []EvidenceRef{}
	for _, ev := range s.Evidence {
		if ev.Valid && ev.Kind == ref.Kind && ev.Ref == ref.Ref && ev.SHA256 == ref.SHA256 {
			ranges = append(ranges, ev.EvidenceRef)
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	covered := ref.Start
	found := false
	for _, ev := range ranges {
		if ev.Start > covered {
			break
		}
		if ev.End >= covered {
			found = true
			covered = max(covered, ev.End)
		}
		if found && covered >= ref.End {
			return true
		}
	}
	return false
}
func (s *state) addEvidence(ref EvidenceRef) error {
	if s.hasEvidence(ref) {
		return nil
	}
	if len(s.Evidence) >= maxRecords {
		return errors.New("task_evidence_capacity")
	}
	// Count progress only for newly delivered valid ranges, never Host prose.
	s.Evidence = append(s.Evidence, evidence{EvidenceRef: ref, Valid: true})
	s.Progress++
	return nil
}
func (s *state) addPending(value string) error {
	for _, p := range s.Pending {
		if p == value {
			return nil
		}
	}
	if len(s.Pending) >= maxRecords {
		return errors.New("task_pending_capacity")
	}
	s.Pending = append(s.Pending, value)
	return nil
}

func (s *state) summary() Summary {
	valid := 0
	for _, e := range s.Evidence {
		if e.Valid {
			valid++
		}
	}
	return Summary{Goal: s.Goal, Phase: s.Phase, BudgetBytes: s.Budget, UsedBytes: s.Used, ReserveBytes: ReserveBytes, PageBytes: s.Page, EvidenceCount: valid, InvalidEvidenceCount: len(s.Evidence) - valid, PendingCount: len(s.Pending), Progress: s.Progress, CompletedBatchCount: len(s.CompletedBatches), LastBatchID: s.LastBatchID, DecisionCount: len(s.Resolutions), MapVersion: s.Map.Version, MapValid: s.Map.Valid, ConfigSHA256: s.ConfigHash, IndexIdentity: s.IndexIdentity}
}
func (s *state) response(status string) *Response {
	r := &Response{Status: status, TaskID: s.TaskID, State: s.summary(), Metering: "serialized_task_responses_plus_explicit_external_bytes"}
	if !s.Map.Valid {
		r.Next = "map_missing: read necessary evidence, then submit a Host-authored map"
		if s.Map.Version > 0 {
			r.Next = "map_stale: read the retained map and recheck changed evidence before updating it"
		}
	}
	return r
}
func statePath(id string) string { return ".aoci/task-context/" + id + ".json" }
func save(root string, s *state) error {
	path, err := regularPath(root, statePath(s.TaskID), true)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if _, err = regularPath(root, statePath(s.TaskID), true); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return afs.AtomicWriteMode(path, b, 0600)
}
func load(root, id string) (*state, error) {
	b, err := readRegular(root, statePath(id))
	if err != nil {
		return nil, err
	}
	var s state
	if err = json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Version != 1 || s.Root != root || s.TaskID != id || s.Budget < 2*ReserveBytes || s.Page < 256 || s.Page > DefaultPageBytes || s.Used < 0 || len(s.Evidence) > maxRecords || len(s.Pending) > maxRecords || len(s.Resolutions) > maxRecords {
		return nil, fmt.Errorf("task_state_invalid")
	}
	return &s, nil
}

func sortedObjects(e *environment) []cognition.Object {
	out := make([]cognition.Object, 0, len(e.objects))
	for _, o := range e.objects {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CanonicalRef < out[j].CanonicalRef })
	return out
}
