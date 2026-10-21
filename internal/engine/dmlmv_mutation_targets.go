package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func mutationPhysicalRow(source *exec.Source, index int, b analyze.Binding) exec.QueryPhysicalRow {
	version := source.VersionAt(source.RawIndex(index))
	return source.PhysicalRow(index, exec.Leaf{Ref: b.Ref, Relation: b.Relation, Storage: b.Storage}, version)
}

type mutationRowIdentity struct {
	ref ids.ObjectRef
	row ids.RowID
}

func scatterMutationRow(physical exec.QueryPhysicalRow, old storage.RowVersion, named []sqltype.Value, targets []int) []sqltype.Value {
	if physical.Segment == nil || physical.Segment.Layout == nil {
		return named
	}
	row := append([]sqltype.Value(nil), old.Values...)
	for _, slot := range targets {
		row[physical.Segment.Layout[slot]] = named[slot]
	}
	return row
}

// versionRows appends the logical row identities of versions to buf.
func versionRows(buf []ids.RowID, versions []storage.RowVersion) []ids.RowID {
	for _, version := range versions {
		buf = append(buf, version.Row)
	}
	return buf
}
