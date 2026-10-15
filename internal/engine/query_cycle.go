package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A recipe is immutable and occurrence-owned. It never contains a frontier or
// a visited set: those belong to the existing recursive producer invocation.
type cycleRecipe struct {
	syntax         *parser.CycleClause
	shapeError     error // Deferred until body analysis establishes diagnostic precedence.
	width          int
	keys           []int
	grouping       []sqltype.Grouping
	record, path   *sqltype.Descriptor
	mark, fallback BoundExpr
	// Typed introducers retain their analyzed source Usage before common-output
	// coercion, so replay reconstructs the original input conversion.
	typedConstants [2]sqltype.Usage
	inequality     sqltype.ComparisonPredicate
}

func (c *cycleRecipe) equalRecord(ctx context.Context, a, b sqltype.Value) (bool, error) {
	if a.Type() != c.record || b.Type() != c.record || a.IsNull() || b.IsNull() {
		return false, fmt.Errorf("stuntgres: foreign cycle record")
	}
	for i, g := range c.grouping {
		equal, err := g.Equal(ctx, a.CompositeField(i), b.CompositeField(i))
		if err != nil || !equal {
			return equal, err
		}
	}
	return true, ctx.Err()
}
func (c *cycleRecipe) equalPath(ctx context.Context, a, b sqltype.Value) (bool, error) {
	if a.Type() != c.path || b.Type() != c.path || a.IsNull() || b.IsNull() {
		return false, fmt.Errorf("stuntgres: foreign cycle path")
	}
	if a.ArrayLen() != b.ArrayLen() {
		return false, ctx.Err()
	}
	if a.ArrayRank() != b.ArrayRank() {
		return false, ctx.Err()
	}
	for i := range a.ArrayRank() {
		if a.ArrayDimension(i) != b.ArrayDimension(i) {
			return false, ctx.Err()
		}
	}
	for i := range a.ArrayLen() {
		equal, err := c.equalRecord(ctx, a.ArrayElement(i), b.ArrayElement(i))
		if err != nil || !equal {
			return equal, err
		}
	}
	return true, ctx.Err()
}
func (c *cycleRecipe) hashPath(ctx context.Context, path sqltype.Value) (uint64, error) {
	if path.Type() != c.path || path.IsNull() {
		return 0, fmt.Errorf("stuntgres: foreign cycle path")
	}
	hash := uint64(14695981039346656037)
	for i := range path.ArrayRank() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		dim := path.ArrayDimension(i)
		hash = (hash ^ uint64(uint32(dim.Lower))) * 1099511628211
		hash = (hash ^ uint64(uint32(dim.Length))) * 1099511628211
	}
	for i := range path.ArrayLen() {
		record := path.ArrayElement(i)
		for j, g := range c.grouping {
			part, err := g.Hash(ctx, record.CompositeField(j))
			if err != nil {
				return 0, err
			}
			hash = (hash ^ part) * 1099511628211
		}
	}
	return hash, ctx.Err()
}
func (c *cycleRecipe) augment(ctx context.Context, row []sqltype.Value, parent sqltype.Value, mark, fallback sqltype.Value) ([]sqltype.Value, error) {
	fields := make([]sqltype.Value, len(c.keys))
	for i, key := range c.keys {
		value, err := c.grouping[i].PrepareKey(ctx, row[key])
		if err != nil {
			return nil, err
		}
		fields[i] = value
	}
	record, err := sqltype.OwnedCompositeValue(c.record, fields)
	if err != nil {
		return nil, err
	}
	length := 0
	if parent.Type() != nil {
		if parent.Type() != c.path || parent.IsNull() {
			return nil, fmt.Errorf("stuntgres: invalid cycle parent")
		}
		length = parent.ArrayLen()
	}
	elements := make([]sqltype.Value, length+1)
	value := fallback
	detected := false
	for i := range length {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		elements[i] = parent.ArrayElement(i)
		if !detected {
			equal, err := c.equalRecord(ctx, record, elements[i])
			if err != nil {
				return nil, err
			}
			if equal {
				value = mark
				detected = true
			}
		}
	}
	elements[length] = record
	path, err := sqltype.OwnedArrayValue(c.path, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
	if err != nil {
		return nil, err
	}
	return append(row[:c.width], value, path), ctx.Err()
}

// Hidden parent projections use the actual joined worktable occurrence, never
// generation order. The legality walker guarantees exactly one top-level self.
func cycleParentSlot(node LogicalNode, d *cteDefinition, offset int) (*worktableScanNode, int) {
	switch n := node.(type) {
	case *worktableScanNode:
		if n.cte == d {
			return n, offset
		}
	case *joinNode:
		if scan, slot := cycleParentSlot(n.left, d, offset); scan != nil {
			return scan, slot
		}
		return cycleParentSlot(n.right, d, offset+n.leftWidth)
	default:
		left, _ := queryInputs(node)
		if left != nil {
			return cycleParentSlot(left, d, offset)
		}
	}
	return nil, 0
}
