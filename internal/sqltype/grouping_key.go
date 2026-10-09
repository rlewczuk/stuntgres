package sqltype

import (
	"context"
	"slices"
)

// PrepareKey retains the typed datum while freezing the binary-compatible text
// equality representation of structural trees. A DISTINCT occurrence calls it
// once before hashing; retained output can reuse the exact same immutable text.
// Ordinary scalar/array keys need no additional representation or allocation.
func (g Grouping) PrepareKey(ctx context.Context, v Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if err := g.check(v); err != nil {
		return Value{}, err
	}
	if v.null || !g.prepare {
		return v, nil
	}
	if g.element != nil {
		var elements []Value
		for i, old := range v.array.elements {
			next, err := g.element.PrepareKey(ctx, old)
			if err != nil {
				return Value{}, err
			}
			if next != old && elements == nil {
				elements = slices.Clone(v.array.elements)
			}
			if elements != nil {
				elements[i] = next
			}
		}
		if elements != nil {
			v.array = &arrayPayload{dimensions: v.array.dimensions, elements: elements}
		}
		return v, ctx.Err()
	}
	if !v.treeKey {
		text, err := v.typ.Output(ctx, v)
		if err != nil {
			return Value{}, err
		}
		v.text, v.treeKey = text, true
	}
	return v, ctx.Err()
}
