package cognition

import (
	"reflect"
	"testing"

	"github.com/aoci-spec/aoci-code/internal/index"
)

func taskObject(ref, name, relations string) Object {
	return Object{CanonicalRef: ref, Name: name, VolumeID: "code", Entry: &index.Entry{R: relations}}
}

func TestTaskRelationsOneHopCycleAndReverse(t *testing.T) {
	set := &Set{LayoutMode: LayoutLegacyMonolithic, Root: Asset{Objects: []Object{
		taskObject("code:a", "a", "b,b"), taskObject("code:b", "b", "a,c"),
		taskObject("code:c", "c", "d"), taskObject("code:d", "d", "-"),
	}}}
	want := []TaskRelation{
		{From: "code:a", To: "code:b", Token: "b", Resolved: true},
		{From: "code:b", To: "code:a", Token: "a", Resolved: true},
	}
	if got := TaskRelations(set, []string{"code:a", "code:a"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("only direct forward/reverse annotations, deduplicated: got %#v", got)
	}
}

func TestTaskRelationsPreservesAmbiguousAndMissingClues(t *testing.T) {
	set := &Set{LayoutMode: LayoutLegacyMonolithic, Root: Asset{Objects: []Object{
		taskObject("code:a", "a", "same, code:missing, unknown"),
		taskObject("code:b", "same", "-"), taskObject("code:c", "same", "-"),
	}}}
	want := []TaskRelation{
		{From: "code:a", Token: "same"}, {From: "code:a", Token: "unknown"},
		{From: "code:a", To: "code:missing", Token: "code:missing"},
	}
	if got := TaskRelations(set, []string{"code:a"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("unresolved annotations are clues, not discarded or guessed: %#v", got)
	}
	if got := TaskRelations(set, []string{"code:missing"}); !reflect.DeepEqual(got, want[2:]) {
		t.Fatalf("an unindexed task ref still has explicit reverse clues: %#v", got)
	}
	if got := TaskRelations(set, []string{"code:b"}); len(got) != 0 {
		t.Fatalf("an ambiguous name must not create reverse edges: %#v", got)
	}
	if len(set.Errors) != 0 || len(set.Warnings) != 0 {
		t.Fatal("reading clues must not alter index diagnostics")
	}
}

func TestTaskRelationsUsesDatabaseNamespaceAndVolumes(t *testing.T) {
	first := taskObject("database://db/one/a", "a", "target")
	first.VolumeID, first.Namespace = "database", "one"
	local := taskObject("database://db/one/target", "target", "-")
	local.VolumeID, local.Namespace = "database", "one"
	other := taskObject("database://db/two/target", "target", "-")
	other.VolumeID, other.Namespace = "database", "two"
	set := &Set{LayoutMode: LayoutVolumesV1, DeclaredOrder: []string{"database"}, Volumes: map[string]*Asset{
		"database": {State: AssetPresent, Objects: []Object{other, first, local}},
	}}
	want := []TaskRelation{{From: first.CanonicalRef, To: local.CanonicalRef, Token: "target", Resolved: true}}
	if got := TaskRelations(set, []string{local.CanonicalRef}); !reflect.DeepEqual(got, want) {
		t.Fatalf("database-local resolution must match Impact: %#v", got)
	}
	if got := TaskRelations(nil, []string{"code:a"}); len(got) != 0 {
		t.Fatalf("missing index must produce an empty clue set: %#v", got)
	}
}
