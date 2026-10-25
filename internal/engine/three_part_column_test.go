package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
)

// Scalar unit fixtures explicitly admit their input row, just like production owners.
func bindTestTargetNamespace(b *analyze.Binder) {
	relation := b.Target.Relation
	columns := make([]analyze.QueryColumn, len(relation.Columns))
	for i, column := range relation.Columns {
		columns[i] = analyze.QueryColumn{Descriptor: column, Slot: i, Physical: i, Leaf: analyze.QueryTargetLeaf}
	}
	qualifier := analyze.QueryQualifier{Name: relation.Name(), Original: relation.Name(), Ref: b.Target.Ref, Columns: columns}
	if relation.Namespace() != (ids.ObjectAddress{}) {
		qualifier.Schema = b.BindingCatalog().Get(relation.Namespace()).Name()
	}
	b.Query.Namespace = &analyze.QueryNamespace{Columns: columns, Qualifiers: []analyze.QueryQualifier{qualifier}, Frame: relation.Columns}
}
