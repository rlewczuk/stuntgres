package engine

import (
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryOuterJoinsAbsentVersionsAndLineage(t *testing.T) {
	real := storage.RowVersion{Row: 7}
	source := &exec.Source{Materialized: true, Occurrences: []int{0, 1}, Provenance: []int{-1, 3}, Versions: []storage.RowVersion{{}, real}}
	got := exec.AppendQueryVersions(nil, source, 0, &exec.LockContext{})
	if !reflect.DeepEqual(got, []storage.RowVersion{{}, real}) {
		t.Fatal("first absent version invalidated complete cache", got)
	}
	bridge := &exec.Source{Lineage: &exec.QueryLockLineage{Stage: source, CurrentIndex: 4, CurrentOrder: []sqltype.Value{{}}, CacheWidth: 1}}
	provenance := exec.AppendQueryProvenance(nil, bridge, exec.AbsentQueryIndex)
	if !reflect.DeepEqual(provenance, []int{-1, -1}) || bridge.Lineage.CurrentIndex != -1 || bridge.Lineage.CurrentOrder != nil {
		t.Fatal(provenance, bridge.Lineage)
	}
	if source.RawIndex(-1) != -1 || source.VersionAt(-1).Row != 0 || source.Row(-1) != nil {
		t.Fatal("absent identity dereferenced")
	}
	if len(exec.AppendQueryVersions(nil, bridge, -1, &exec.LockContext{})) != 2 {
		t.Fatal("absent version width")
	}
	sparse := &exec.Source{Materialized: true, Occurrences: []int{0, 1}, Versions: []storage.RowVersion{{}, {}}, VersionValid: []bool{false}, Lineage: &exec.QueryLockLineage{Stage: source, Selection: []int{0}}}
	got = exec.AppendQueryVersions(nil, sparse, 0, &exec.LockContext{})
	if !reflect.DeepEqual(got, []storage.RowVersion{{}, real}) {
		t.Fatal("sparse uninitialized cache mistaken for absent physical versions", got)
	}
}

func TestQueryOuterJoinsLocalMarkFrontierManifest(t *testing.T) {
	inputs := &exec.Source{Sources: []*exec.Source{{Occurrence: 0}, {Occurrence: 1}}}
	inputs.Locks = &exec.LockContext{Leaves: []exec.QueryLockLeaf{
		{Leaf: exec.Leaf{Relation: &catalog.Relation{RelationKind: catalog.TableRelation}}},
		{Leaf: exec.Leaf{Relation: &catalog.Relation{RelationKind: catalog.TableRelation}}},
	}}
	left := &plan.RowLockNode{Child: &plan.SourceNode{Occurrence: 0}, Marks: []plan.RowMark{{Mode: lock.ForUpdate}}, FirstMode: lock.ForUpdate}
	for _, kind := range []parser.JoinKind{parser.JoinInner, parser.JoinLeft, parser.JoinRight, parser.JoinFull} {
		join := &plan.JoinNode{Kind: kind, Left: left, Right: &plan.SourceNode{Occurrence: 1}}
		marks := exec.PendingQueryMarks(join, inputs, nil)
		want := 0
		if kind == parser.JoinInner {
			want = 1
		}
		if len(marks) != want {
			t.Fatal(kind, marks)
		}
	}
}
