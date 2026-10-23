package dml

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func MutationPhysicalRow(source *exec.Source, index int, b analyze.Binding) exec.QueryPhysicalRow {
	version := source.VersionAt(source.RawIndex(index))
	return source.PhysicalRow(index, exec.Leaf{Ref: b.Ref, Relation: b.Relation, Storage: b.Storage}, version)
}

type MutationRowIdentity struct {
	Ref ids.ObjectRef
	Row ids.RowID
}

func ScatterMutationRow(physical exec.QueryPhysicalRow, old storage.RowVersion, named []sqltype.Value, targets []int) []sqltype.Value {
	if physical.Segment == nil || physical.Segment.Layout == nil {
		return named
	}
	row := append([]sqltype.Value(nil), old.Values...)
	for _, slot := range targets {
		row[physical.Segment.Layout[slot]] = named[slot]
	}
	return row
}

// BeforeMutationUpdate preserves lexical targets independently of hook changes.
// Mapped named rows are projected from the finalized physical tuple after generation.
func BeforeMutationUpdate(h Host, b analyze.Binding, physical exec.QueryPhysicalRow, old storage.RowVersion, named, row []sqltype.Value) (BeforeResult, error) {
	relation := physical.Leaf.Relation
	if !relation.HasTriggers() {
		return BeforeResult{Row: row, Proceed: true}, nil
	}
	var layout []int
	if physical.Segment != nil {
		layout = physical.Segment.Layout
	}
	targets := UpdateTargetAttributes(h, physical.Leaf.Ref, relation, b.Targets, layout)
	result, err := Before(h, relation, BeforeRow{Relation: physical.Leaf.Ref, Event: BeforeUpdate, Old: old.Values, New: row, Targets: targets})
	if err != nil || !result.Proceed {
		return result, err
	}
	if physical.Segment == nil || physical.Segment.Layout == nil {
		copy(named, result.Row)
		// Preserve the named/physical alias through final generation. Returning
		// the hook's separate tuple here would leave named RETURNING with NULL g.
		result.Row = named
	}
	return result, nil
}

// VersionRows appends the logical row identities of versions to buf.
func VersionRows(buf []ids.RowID, versions []storage.RowVersion) []ids.RowID {
	for _, version := range versions {
		buf = append(buf, version.Row)
	}
	return buf
}
