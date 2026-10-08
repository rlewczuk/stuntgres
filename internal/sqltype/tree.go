package sqltype

import (
	"context"
	"fmt"
)

// StructuralTree is an immutable engine-owned syntax payload, not SQL text.
// Implementations must never evaluate the expression when producing output.
type StructuralTree interface {
	StructuralOutput(context.Context) (string, error)
}
type treePayload struct{ value StructuralTree }

func Tree(d *Descriptor, tree StructuralTree) (Value, error) {
	if d == nil || d.ID() != PgNodeTree || tree == nil {
		return Value{}, fmt.Errorf("invalid structural tree")
	}
	return Value{typ: d, tree: &treePayload{tree}}, nil
}
func (v Value) Tree() (StructuralTree, error) {
	if v.typ == nil || v.typ.ID() != PgNodeTree || v.null || v.tree == nil {
		return nil, fmt.Errorf("not a structural tree value")
	}
	return v.tree.value, nil
}
