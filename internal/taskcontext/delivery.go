package taskcontext

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/aoci-spec/aoci-code/internal/cognition"
	afs "github.com/aoci-spec/aoci-code/internal/fs"
)

type cursor struct {
	Binding string `json:"b"`
	Offset  int    `json:"o"`
}

func encodeCursor(binding string, offset int) string {
	b, _ := json.Marshal(cursor{binding, offset})
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeCursor(raw, binding string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, errors.New("task_cursor_invalid")
	}
	var c cursor
	if json.Unmarshal(b, &c) != nil || c.Binding != binding || c.Offset < 0 {
		return 0, errors.New("task_cursor_stale_or_mismatched")
	}
	return c.Offset, nil
}
func pageLimit(s *state, req Request) int {
	n := s.Page
	if req.LimitBytes > 0 && req.LimitBytes < n {
		n = req.LimitBytes
	}
	return n
}
func responseSize(r *Response) int {
	b, _ := json.Marshal(r)
	return len(b) + 24 /* final counter growth */
}
func fit(s *state, r *Response, limit int) bool {
	n := responseSize(r)
	// Stop itself is a serialized response too. Leave its envelope within the
	// material allowance so the checkpoint reserve remains available intact.
	stopBytes := responseSize(s.response("budget_stop")) + 64
	return n <= limit && n <= s.Budget-ReserveBytes-s.Used-stopBytes
}

func deliver(s *state, e *environment, req Request) (*Response, error) {
	if s.Used > s.Budget-ReserveBytes-1024 {
		r := s.response("budget_stop")
		r.NextCursor = req.Cursor
		r.Next = "checkpoint, then resume with a new Host event"
		return r, nil
	}
	if req.Action == "read" && (req.Kind == "source" || req.Kind == "meta" || req.Kind == "map" || req.Kind == "result") {
		return readPage(s, e, req)
	}
	items := []Item{}
	switch req.Action {
	case "read":
		if req.Kind != "entry" {
			return nil, errors.New("task_read_kind_invalid")
		}
		if len(req.Refs) == 0 {
			return nil, errors.New("task_entry_refs_required")
		}
		seen := map[string]bool{}
		for _, ref := range req.Refs {
			if seen[ref] {
				continue
			}
			seen[ref] = true
			b, err := e.current(EvidenceRef{Kind: "entry", Ref: ref})
			if err != nil {
				return nil, err
			}
			ev := EvidenceRef{Kind: "entry", Ref: ref, SHA256: digest(b), End: len(b)}
			items = append(items, Item{Kind: "entry", Ref: ref, Text: string(b), Evidence: &ev})
		}
	case "search":
		if req.Kind != "" && req.Kind != "entry" && req.Kind != "source" {
			return nil, errors.New("task_search_kind_invalid")
		}
		q := strings.ToLower(strings.TrimSpace(req.Query))
		if q == "" {
			return nil, errors.New("task_search_query_required")
		}
		// Search is a selection list. It does not claim the entry body was read.
		if req.Kind == "source" {
			paths := []string{}
			for p, allowed := range e.allowed {
				if allowed && strings.Contains(strings.ToLower(p), q) {
					paths = append(paths, p)
				}
			}
			sort.Strings(paths)
			for _, p := range paths {
				items = append(items, Item{Kind: "source_match", Ref: p})
			}
		} else {
			for _, o := range sortedObjects(e) {
				if strings.Contains(strings.ToLower(o.CanonicalRef+" "+o.CanonicalLine), q) {
					items = append(items, Item{Kind: "entry_match", Ref: o.CanonicalRef})
				}
			}
		}
	case "relations":
		if len(req.Refs) == 0 {
			return nil, errors.New("task_relation_refs_required")
		}
		if e.set != nil {
			for _, rel := range cognition.TaskRelations(e.set, req.Refs) {
				items = append(items, Item{Kind: "relation", Ref: rel.From, From: rel.From, To: rel.To, Token: rel.Token, Resolved: rel.Resolved})
			}
		}
	}
	bind := jsonHash(struct {
		Action, Kind, Query, Identity, ConfigHash, SelectionHash string
		Refs                                                     []string
	}{req.Action, req.Kind, req.Query, e.identity, e.cfgHash, jsonHash(items), req.Refs})
	return listPage(s, req, items, bind, true)
}

func readPage(s *state, e *environment, req Request) (*Response, error) {
	ref := req.Path
	if req.Kind == "source" {
		var err error
		ref, err = afs.NormalizeRelPath(ref)
		if err != nil {
			return nil, err
		}
	}
	if req.Kind == "meta" {
		ref = "meta"
	}
	var data []byte
	var err error
	previousMap := s.Map
	if req.Kind == "map" {
		// Refresh the shared map by reading it, without inventing a Host event
		// or resetting the phase budget. A failed/stale cursor saves no state.
		s.Map, _, err = loadSharedMap(e.root)
		if err != nil {
			return nil, err
		}
		if err = s.reconcile(e); err != nil {
			return nil, err
		}
		if s.Map.Text == "" {
			return s.response("map_missing"), nil
		}
		ref = "map"
		data = []byte(s.Map.Text)
	} else if req.Kind == "result" {
		data, err = readOutput(e.root, s.TaskID, ref)
		if err != nil {
			return nil, err
		}
	} else {
		data, err = e.current(EvidenceRef{Kind: req.Kind, Ref: ref})
		if err != nil {
			return nil, err
		}
	}
	if !utf8.Valid(data) {
		return nil, errors.New("task_material_utf8_invalid")
	}
	hash := digest(data)
	binding := jsonHash([]string{req.Kind, ref, hash})
	start, err := decodeCursor(req.Cursor, binding)
	if err != nil {
		return nil, err
	}
	if start > len(data) || (start < len(data) && !utf8.RuneStart(data[start])) {
		return nil, errors.New("task_cursor_range_invalid")
	}
	limit := pageLimit(s, req)
	r := s.response("ok")
	if req.Kind == "map" && !s.Map.Valid {
		r.Status = "map_stale"
	}
	// Find the largest UTF-8 complete byte range whose serialized envelope fits.
	upper := min(len(data), start+limit)
	for upper > start && upper < len(data) && !utf8.RuneStart(data[upper]) {
		upper--
	}
	lo, hi := start, upper
	best := -1
	build := func(end int) {
		ev := EvidenceRef{Kind: req.Kind, Ref: ref, SHA256: hash, Start: start, End: end}
		r.Items = []Item{{Kind: req.Kind, Ref: ref, Text: string(data[start:end]), Evidence: &ev}}
		r.NextCursor = ""
		if end < len(data) {
			r.NextCursor = encodeCursor(binding, end)
		}
		r.Remaining = len(data) - end
	}
	for lo <= hi {
		mid := (lo + hi) / 2
		for mid > start && mid < len(data) && !utf8.RuneStart(data[mid]) {
			mid--
		}
		build(mid)
		if fit(s, r, limit) {
			best = mid
			lo = max(mid+1, (lo+hi)/2+1)
		} else {
			hi = mid - 1
		}
	}
	if best < start || (best == start && start < len(data)) {
		if req.Kind == "map" {
			s.Map = previousMap
		}
		r = s.response("capacity_stop")
		r.NextCursor = encodeCursor(binding, start)
		r.Remaining = len(data) - start
		r.Next = "increase the page limit or checkpoint and resume; no material delivered"
		return r, nil
	}
	build(best)
	if req.Kind != "map" {
		if err = s.addEvidence(*r.Items[0].Evidence); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func listPage(s *state, req Request, items []Item, binding string, evidenceDelivery bool) (*Response, error) {
	offset, err := decodeCursor(req.Cursor, binding)
	if err != nil {
		return nil, err
	}
	if offset > len(items) {
		return nil, errors.New("task_cursor_range_invalid")
	}
	r := s.response("ok")
	limit := pageLimit(s, req)
	next := offset
	for next < len(items) {
		r.Items = append(r.Items, items[next])
		r.Remaining = len(items) - next - 1
		r.NextCursor = ""
		if next+1 < len(items) {
			r.NextCursor = encodeCursor(binding, next+1)
		}
		if !fit(s, r, limit) {
			r.Items = r.Items[:len(r.Items)-1]
			break
		}
		next++
	}
	r.Remaining = len(items) - next
	r.NextCursor = ""
	if next < len(items) {
		r.NextCursor = encodeCursor(binding, next)
	}
	if next == offset && next < len(items) {
		r.Status = "capacity_stop"
		r.Next = "one item exceeds the available page or phase capacity; request source evidence or a smaller task"
		return r, nil
	}
	if evidenceDelivery {
		for _, item := range r.Items {
			if item.Evidence != nil {
				if err = s.addEvidence(*item.Evidence); err != nil {
					return nil, err
				}
			}
			if item.Kind == "relation" && !item.Resolved {
				pending := "relation:" + item.From + ":" + item.Token
				if len(s.Pending) >= maxRecords {
					return nil, errors.New("task_pending_capacity")
				}
				if err = s.addPending(pending); err != nil {
					return nil, err
				}
			}
		}
	}
	return r, nil
}

func statusPage(s *state, req Request) (*Response, error) {
	if req.Kind == "" {
		return s.response("ok"), nil
	}
	items := []Item{}
	switch req.Kind {
	case "pending":
		for _, p := range s.Pending {
			items = append(items, Item{Kind: "pending", Ref: p})
		}
	case "evidence":
		for _, ev := range s.Evidence {
			copy := ev.EvidenceRef
			kind := "valid_evidence"
			if !ev.Valid {
				kind = "invalid_evidence"
			}
			items = append(items, Item{Kind: kind, Ref: ev.Ref, Evidence: &copy})
		}
	case "checkpoint":
		if s.Note != "" {
			items = append(items, Item{Kind: "checkpoint", Ref: "note", Text: s.Note})
		}
	case "decisions":
		for i, decision := range s.Resolutions {
			copy := decision
			items = append(items, Item{Kind: "decision", Ref: strconv.Itoa(i + 1), Decision: &copy})
		}
	default:
		return nil, fmt.Errorf("task_status_kind_invalid: %s", req.Kind)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Ref < items[j].Ref })
	if req.Kind == "decisions" {
		view := *s
		view.Page = DefaultPageBytes
		return listPage(&view, req, items, jsonHash(items), false)
	}
	return listPage(s, req, items, jsonHash(items), false)
}
