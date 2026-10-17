package sqltype

import (
	"context"
	"fmt"
	"sync/atomic"
)

// StructuralTree is an immutable engine-owned syntax payload, not SQL text.
// Implementations must never evaluate the expression when producing output.
type StructuralTree interface {
	StructuralOutput(context.Context) (string, error)
}
type treePayload struct {
	value StructuralTree
	text  atomic.Pointer[string]
}

// Output is immutable for a payload. Keep retention lazy and publish only
// successful rendering; callbacks run without locks and cancellation never
// poisons a value shared by later comparisons or retained readers.
func (p *treePayload) output(ctx context.Context) (string, error) {
	if text := p.text.Load(); text != nil {
		return *text, nil
	}
	text, err := p.value.StructuralOutput(ctx)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.text.CompareAndSwap(nil, &text)
	return text, nil
}

func Tree(d *Descriptor, tree StructuralTree) (Value, error) {
	if d == nil || d.ID() != PgNodeTree || tree == nil {
		return Value{}, fmt.Errorf("invalid structural tree")
	}
	return Value{typ: d, tree: &treePayload{value: tree}}, nil
}
func (v Value) Tree() (StructuralTree, error) {
	if v.typ == nil || v.typ.ID() != PgNodeTree || v.null || v.tree == nil {
		return nil, fmt.Errorf("not a structural tree value")
	}
	return v.tree.value, nil
}
