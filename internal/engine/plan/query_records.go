package plan

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// CTEMutation is the opaque payload a data-modifying CTE definition carries.
// Plan code only tests it for presence; the engine recovers the analyzed
// statement binding through mutationBinding. A definition never stores a
// typed nil, so a nil payload keeps meaning "a query CTE".
type CTEMutation interface{ CTEMutationPayload() }

// A definition is immutable after statement analysis. Only invocation owners
// contain producer state; neither catalog objects nor prepared handles do.
type CTEDefinition struct {
	Query      *Query
	Statement  parser.Statement
	Mutation   CTEMutation
	Schema     QuerySchema
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
	Search     *SearchRecipe
	Cycle      *CycleRecipe
}

func (d *CTEDefinition) Folded() bool {
	return !d.Recursive && d.Mutation == nil && !d.Volatile && d.Hint != parser.CTEMaterialized && (d.Hint == parser.CTENotMaterialized || d.References == 1)
}

func (d *CTEDefinition) OutputSchema() QuerySchema {
	if d.Query != nil {
		return d.Query.Root.Schema()
	}
	return d.Schema
}

// A recipe is immutable and occurrence-owned. It never contains a frontier or
// a visited set: those belong to the existing recursive producer invocation.
type CycleRecipe struct {
	Syntax         *parser.CycleClause
	ShapeError     error // Deferred until body analysis establishes diagnostic precedence.
	MarkSlot       int
	Keys           []int
	Grouping       []Grouping
	Record, Path   *sqltype.Descriptor
	Mark, Fallback expr.Expr
	// Typed introducers retain their analyzed source Usage before common-output
	// coercion, so replay reconstructs the original input conversion.
	TypedConstants [2]sqltype.Usage
	Inequality     sqltype.ComparisonPredicate
}

func (c *CycleRecipe) equalRecord(ctx context.Context, checkpoint func(context.Context) error, a, b sqltype.Value, evaluation expr.Context) (bool, error) {
	if a.Type() != c.Record || b.Type() != c.Record || a.IsNull() || b.IsNull() {
		return false, fmt.Errorf("stuntgres: foreign cycle record")
	}
	if err := checkpoint(ctx); err != nil {
		return false, err
	}
	for i, g := range c.Grouping {
		if err := checkpoint(ctx); err != nil {
			return false, err
		}
		equal, err := g.EqualWithEvaluation(ctx, a.CompositeField(i), b.CompositeField(i), evaluation)
		if err != nil {
			return false, err
		}
		if err := checkpoint(ctx); err != nil {
			return false, err
		}
		if !equal {
			return false, nil
		}
	}
	return true, checkpoint(ctx)
}

func (c *CycleRecipe) EqualPath(ctx context.Context, checkpoint func(context.Context) error, a, b sqltype.Value, evaluation expr.Context) (bool, error) {
	if a.Type() != c.Path || b.Type() != c.Path || a.IsNull() || b.IsNull() {
		return false, fmt.Errorf("stuntgres: foreign cycle path")
	}
	if err := checkpoint(ctx); err != nil {
		return false, err
	}
	if a.ArrayLen() != b.ArrayLen() {
		return false, checkpoint(ctx)
	}
	if a.ArrayRank() != b.ArrayRank() {
		return false, checkpoint(ctx)
	}
	for i := range a.ArrayRank() {
		if err := checkpoint(ctx); err != nil {
			return false, err
		}
		if a.ArrayDimension(i) != b.ArrayDimension(i) {
			return false, checkpoint(ctx)
		}
	}
	for i := range a.ArrayLen() {
		equal, err := c.equalRecord(ctx, checkpoint, a.ArrayElement(i), b.ArrayElement(i), evaluation)
		if err != nil || !equal {
			return equal, err
		}
	}
	return true, checkpoint(ctx)
}

func (c *CycleRecipe) HashPath(ctx context.Context, checkpoint func(context.Context) error, path sqltype.Value, evaluation expr.Context) (uint64, error) {
	if path.Type() != c.Path || path.IsNull() {
		return 0, fmt.Errorf("stuntgres: foreign cycle path")
	}
	if err := checkpoint(ctx); err != nil {
		return 0, err
	}
	hash := uint64(14695981039346656037)
	for i := range path.ArrayRank() {
		if err := checkpoint(ctx); err != nil {
			return 0, err
		}
		dim := path.ArrayDimension(i)
		hash = (hash ^ uint64(uint32(dim.Lower))) * 1099511628211
		hash = (hash ^ uint64(uint32(dim.Length))) * 1099511628211
	}
	for i := range path.ArrayLen() {
		if err := checkpoint(ctx); err != nil {
			return 0, err
		}
		record := path.ArrayElement(i)
		for j, g := range c.Grouping {
			if err := checkpoint(ctx); err != nil {
				return 0, err
			}
			part, err := g.HashWithEvaluation(ctx, record.CompositeField(j), evaluation)
			if err != nil {
				return 0, err
			}
			if err := checkpoint(ctx); err != nil {
				return 0, err
			}
			hash = (hash ^ part) * 1099511628211
		}
	}
	return hash, checkpoint(ctx)
}

func (c *CycleRecipe) Augment(ctx context.Context, checkpoint func(context.Context) error, row []sqltype.Value, parent sqltype.Value, mark, fallback sqltype.Value, evaluation expr.Context) ([]sqltype.Value, error) {
	if err := checkpoint(ctx); err != nil {
		return nil, err
	}
	fields := make([]sqltype.Value, len(c.Keys))
	for i, key := range c.Keys {
		if err := checkpoint(ctx); err != nil {
			return nil, err
		}
		value, err := c.Grouping[i].PrepareKey(ctx, row[key])
		if err != nil {
			return nil, err
		}
		if err := checkpoint(ctx); err != nil {
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
		if err := checkpoint(ctx); err != nil {
			return nil, err
		}
		elements[i] = parent.ArrayElement(i)
		if !detected {
			equal, err := c.equalRecord(ctx, checkpoint, record, elements[i], evaluation)
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
	return append(row[:c.MarkSlot], value, path), checkpoint(ctx)
}

// A caller-checked query consumer owns its selected equality identity beside
// the existing immutable provider, in the same key allocation. Internal hash
// consumers and index support keep ordinary sqltype.Grouping.
type Grouping struct {
	sqltype.Grouping
	Routine       ids.ObjectRef
	Procedure     *catalog.Procedure
	RoutineRecipe expr.RoutineRecipeReference // Exact original query-owned occurrence when grafted.
	Source        expr.Info                   // Genuine selected equality source occurrence, no runtime authority.
	PrivateTree   bool
	Conversion    sqltype.CoercionPlan
	Converted     sqltype.Grouping
	Convert       bool
}

func (g Grouping) Equal(ctx context.Context, a, b sqltype.Value) (bool, error) {
	return g.EqualWithEvaluation(ctx, a, b, nil)
}
func (g Grouping) EqualWithEvaluation(ctx context.Context, a, b sqltype.Value, evaluation expr.Context) (bool, error) {
	boundary, _ := evaluation.(sqltype.GroupingBoundary)
	if !g.Convert {
		return g.Grouping.EqualWithBoundary(ctx, a, b, boundary)
	}
	left, err := g.Conversion.Apply(ctx, a, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		return false, err
	}
	right, err := g.Conversion.Apply(ctx, b, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		return false, err
	}
	return g.Converted.EqualWithBoundary(ctx, left, right, boundary)
}

func (g Grouping) Hash(ctx context.Context, value sqltype.Value) (uint64, error) {
	return g.HashWithEvaluation(ctx, value, nil)
}
func (g Grouping) HashWithEvaluation(ctx context.Context, value sqltype.Value, evaluation expr.Context) (uint64, error) {
	boundary, _ := evaluation.(sqltype.GroupingBoundary)
	if !g.Convert {
		return g.Grouping.HashWithBoundary(ctx, value, boundary)
	}
	converted, err := g.Conversion.Apply(ctx, value, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		return 0, err
	}
	return g.Converted.HashWithBoundary(ctx, converted, boundary)
}

// Strength and wait precedence are independent. A manifest belongs to a bound
// query occurrence, never to its physical relation or column namespace.
type RowMark struct {
	Mode           lock.Mode
	Wait           parser.RowLockWait
	DiagnosticMode lock.Mode
	// One-based first insertion order, independent of FROM order and upgrades.
	Order int
}

type SQLJSONArrayQuery struct {
	Raw   parser.SQLJSONExpr
	Input expr.Expr
	Plan  sqltype.JSONArrayAggregatePlan
}
