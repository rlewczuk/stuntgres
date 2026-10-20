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
type cteMutation interface{ CTEMutationPayload() }

// A definition is immutable after statement analysis. Only invocation owners
// contain producer state; neither catalog objects nor prepared handles do.
type cteDefinition struct {
	Query      *BoundQuery
	Statement  parser.Statement
	Mutation   cteMutation
	Schema     querySchema
	Owner      uint32
	References int
	Hint       parser.CTEMaterialization
	Volatile   bool
	Recursive  bool
	Syntax     *parser.Select
	Name       string
	Columns    []parser.Identifier
	Position   parser.Position
	UserWidth  int
	Search     *searchRecipe
	Cycle      *cycleRecipe
}

func (d *cteDefinition) Folded() bool {
	return !d.Recursive && d.Mutation == nil && !d.Volatile && d.Hint != parser.CTEMaterialized && (d.Hint == parser.CTENotMaterialized || d.References == 1)
}

func (d *cteDefinition) OutputSchema() querySchema {
	if d.Query != nil {
		return d.Query.Root.Schema()
	}
	return d.Schema
}

// A recipe is immutable and occurrence-owned. It never contains a frontier or
// a visited set: those belong to the existing recursive producer invocation.
type cycleRecipe struct {
	Syntax         *parser.CycleClause
	ShapeError     error // Deferred until body analysis establishes diagnostic precedence.
	MarkSlot       int
	Keys           []int
	Grouping       []sqltype.Grouping
	Record, Path   *sqltype.Descriptor
	Mark, Fallback expr.Expr
	// Typed introducers retain their analyzed source Usage before common-output
	// coercion, so replay reconstructs the original input conversion.
	TypedConstants [2]sqltype.Usage
	Inequality     sqltype.ComparisonPredicate
}

func (c *cycleRecipe) equalRecord(ctx context.Context, a, b sqltype.Value) (bool, error) {
	if a.Type() != c.Record || b.Type() != c.Record || a.IsNull() || b.IsNull() {
		return false, fmt.Errorf("stuntgres: foreign cycle record")
	}
	for i, g := range c.Grouping {
		equal, err := g.Equal(ctx, a.CompositeField(i), b.CompositeField(i))
		if err != nil || !equal {
			return equal, err
		}
	}
	return true, ctx.Err()
}

func (c *cycleRecipe) EqualPath(ctx context.Context, a, b sqltype.Value) (bool, error) {
	if a.Type() != c.Path || b.Type() != c.Path || a.IsNull() || b.IsNull() {
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

func (c *cycleRecipe) HashPath(ctx context.Context, path sqltype.Value) (uint64, error) {
	if path.Type() != c.Path || path.IsNull() {
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
		for j, g := range c.Grouping {
			part, err := g.Hash(ctx, record.CompositeField(j))
			if err != nil {
				return 0, err
			}
			hash = (hash ^ part) * 1099511628211
		}
	}
	return hash, ctx.Err()
}

func (c *cycleRecipe) Augment(ctx context.Context, row []sqltype.Value, parent sqltype.Value, mark, fallback sqltype.Value) ([]sqltype.Value, error) {
	fields := make([]sqltype.Value, len(c.Keys))
	for i, key := range c.Keys {
		value, err := c.Grouping[i].PrepareKey(ctx, row[key])
		if err != nil {
			return nil, err
		}
		fields[i] = value
	}
	record, err := sqltype.OwnedCompositeValue(c.Record, fields)
	if err != nil {
		return nil, err
	}
	length := 0
	if parent.Type() != nil {
		if parent.Type() != c.Path || parent.IsNull() {
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
	path, err := sqltype.OwnedArrayValue(c.Path, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
	if err != nil {
		return nil, err
	}
	return append(row[:c.MarkSlot], value, path), ctx.Err()
}

// A caller-checked query consumer owns its selected equality identity beside
// the existing immutable provider, in the same key allocation. Internal hash
// consumers and index support keep ordinary sqltype.Grouping.
type queryGrouping struct {
	sqltype.Grouping
	Routine     ids.ObjectRef
	PrivateTree bool
	Conversion  sqltype.CoercionPlan
	Converted   sqltype.Grouping
	Convert     bool
}

func (g queryGrouping) Equal(ctx context.Context, a, b sqltype.Value) (bool, error) {
	if !g.Convert {
		return g.Grouping.Equal(ctx, a, b)
	}
	left, err := g.Conversion.Apply(ctx, a, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		return false, err
	}
	right, err := g.Conversion.Apply(ctx, b, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		return false, err
	}
	return g.Converted.Equal(ctx, left, right)
}

func (g queryGrouping) Hash(ctx context.Context, value sqltype.Value) (uint64, error) {
	if !g.Convert {
		return g.Grouping.Hash(ctx, value)
	}
	converted, err := g.Conversion.Apply(ctx, value, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		return 0, err
	}
	return g.Converted.Hash(ctx, converted)
}

// Strength and wait precedence are independent. A manifest belongs to a bound
// query occurrence, never to its physical relation or column namespace.
type queryRowMark struct {
	Mode           lock.Mode
	Wait           parser.RowLockWait
	DiagnosticMode lock.Mode
	// One-based first insertion order, independent of FROM order and upgrades.
	Order int
}

type sqlJSONArrayQuery struct {
	Raw   parser.SQLJSONExpr
	Input expr.Expr
	Plan  sqltype.JSONArrayAggregatePlan
}
