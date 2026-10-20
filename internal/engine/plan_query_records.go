package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// cteMutation is the opaque payload a data-modifying CTE definition carries.
// Plan code only tests it for presence; the engine recovers the analyzed
// statement binding through mutationBinding. A definition never stores a
// typed nil, so a nil payload keeps meaning "a query CTE".
type cteMutation interface{ cteMutationPayload() }

// A definition is immutable after statement analysis. Only invocation owners
// contain producer state; neither catalog objects nor prepared handles do.
type cteDefinition struct {
	query      *BoundQuery
	statement  parser.Statement
	mutation   cteMutation
	schema     querySchema
	owner      uint32
	references int
	hint       parser.CTEMaterialization
	volatile   bool
	recursive  bool
	syntax     *parser.Select
	name       string
	columns    []parser.Identifier
	position   parser.Position
	userWidth  int
	search     *searchRecipe
	cycle      *cycleRecipe
}

func (d *cteDefinition) folded() bool {
	return !d.recursive && d.mutation == nil && !d.volatile && d.hint != parser.CTEMaterialized && (d.hint == parser.CTENotMaterialized || d.references == 1)
}

func (d *cteDefinition) outputSchema() querySchema {
	if d.query != nil {
		return d.query.Root.Schema()
	}
	return d.schema
}

// A recipe is immutable and occurrence-owned. It never contains a frontier or
// a visited set: those belong to the existing recursive producer invocation.
type cycleRecipe struct {
	syntax         *parser.CycleClause
	shapeError     error // Deferred until body analysis establishes diagnostic precedence.
	markSlot       int
	keys           []int
	grouping       []sqltype.Grouping
	record, path   *sqltype.Descriptor
	mark, fallback expr.Expr
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
	return append(row[:c.markSlot], value, path), ctx.Err()
}

// A caller-checked query consumer owns its selected equality identity beside
// the existing immutable provider, in the same key allocation. Internal hash
// consumers and index support keep ordinary sqltype.Grouping.
type queryGrouping struct {
	sqltype.Grouping
	routine     ids.ObjectRef
	privateTree bool
	conversion  sqltype.CoercionPlan
	converted   sqltype.Grouping
	convert     bool
}

func (g queryGrouping) Equal(ctx context.Context, a, b sqltype.Value) (bool, error) {
	if !g.convert {
		return g.Grouping.Equal(ctx, a, b)
	}
	left, err := g.conversion.Apply(ctx, a, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		return false, err
	}
	right, err := g.conversion.Apply(ctx, b, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		return false, err
	}
	return g.converted.Equal(ctx, left, right)
}

func (g queryGrouping) Hash(ctx context.Context, value sqltype.Value) (uint64, error) {
	if !g.convert {
		return g.Grouping.Hash(ctx, value)
	}
	converted, err := g.conversion.Apply(ctx, value, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		return 0, err
	}
	return g.converted.Hash(ctx, converted)
}

// Strength and wait precedence are independent. A manifest belongs to a bound
// query occurrence, never to its physical relation or column namespace.
type queryRowMark struct {
	mode           lock.Mode
	wait           parser.RowLockWait
	diagnosticMode lock.Mode
	// One-based first insertion order, independent of FROM order and upgrades.
	order int
}

type sqlJSONArrayQuery struct {
	raw   parser.SQLJSONExpr
	input expr.Expr
	plan  sqltype.JSONArrayAggregatePlan
}
