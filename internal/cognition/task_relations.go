package cognition

import (
	"sort"
	"strings"
)

// TaskRelation is a read-only, one-hop clue from an explicit R annotation.
// Unresolved annotations are useful reading hints, never index diagnostics or
// authoring gates. Token preserves the annotation even when it is ambiguous.
type TaskRelation struct {
	From     string `json:"from"`
	To       string `json:"to,omitempty"`
	Token    string `json:"token,omitempty"`
	Resolved bool   `json:"resolved"`
}

// TaskRelations returns only relations directly incident to refs. It reuses
// Impact's tolerant parsing and resolution without following the transitive
// closure or assigning meaning to an annotation. An explicit canonical token
// may point back to a queried object which has no Entry yet; that clue remains
// unresolved rather than disappearing from the task's reading context.
func TaskRelations(set *Set, refs []string) []TaskRelation {
	result := []TaskRelation{}
	if set == nil || len(refs) == 0 {
		return result
	}
	wanted := map[string]bool{}
	for _, ref := range refs {
		wanted[ref] = true
	}
	registry := currentObjectRegistry(set)
	names, namespaces := buildImpactNameIndexes(registry)
	seen := map[TaskRelation]bool{}
	for _, ref := range sortedObjectRefs(registry) {
		object := registry[ref]
		for _, token := range splitImpactRelations(object.Entry) {
			target := resolveImpactRelation(object, token, registry, names, namespaces)
			relation := TaskRelation{From: ref, To: target, Token: token, Resolved: target != ""}
			if target == "" && (strings.HasPrefix(token, "code:") || strings.HasPrefix(token, "database://")) {
				relation.To = token
			}
			if (wanted[ref] || (relation.To != "" && wanted[relation.To])) && !seen[relation] {
				seen[relation] = true
				result = append(result, relation)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if left.From != right.From {
			return left.From < right.From
		}
		if left.To != right.To {
			return left.To < right.To
		}
		return left.Token < right.Token
	})
	return result
}
