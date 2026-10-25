package engine

import (
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"testing"
)

func TestQueryLockPendingTargetsRemainOccurrenceOwned(t *testing.T) {
	inputs := &exec.Source{Sources: []*exec.Source{{Occurrence: 0}, {Occurrence: 1}}}
	inputs.Locks = &exec.LockContext{Leaves: []exec.QueryLockLeaf{
		{Leaf: exec.Leaf{Relation: &catalog.Relation{RelationKind: catalog.TableRelation}}},
		{Leaf: exec.Leaf{Relation: &catalog.Relation{RelationKind: catalog.TableRelation}}},
	}}
	marks := []plan.RowMark{{Mode: lock.ForUpdate, Wait: parser.RowLockSkip}, {Mode: lock.ForShare, Wait: parser.RowLockNowait}}
	pending := exec.AppendPendingRowMarks(nil, marks, inputs)
	if len(pending) != 2 || !reflect.DeepEqual(pending[0].Occurrences, []int{0}) || !reflect.DeepEqual(pending[1].Occurrences, []int{1}) || pending[0].Mark != marks[0] || pending[1].Mark != marks[1] {
		t.Fatal(pending)
	}
	pending[0].Occurrences[0] = 99
	if inputs.Sources[0].Occurrence != 0 || marks[0].Mode != lock.ForUpdate {
		t.Fatal("execution pending state mutated bound/source identity")
	}
}
