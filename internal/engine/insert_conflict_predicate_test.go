package engine

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

type insertConflictProofFixture struct {
	integer, boolean *sqltype.Descriptor
	column           columnExpression
	ctx              EvalContext
}

func insertConflictProofTypes(t *testing.T) insertConflictProofFixture {
	t.Helper()
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	return insertConflictProofFixture{integer: integer, boolean: boolean, column: columnExpression{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, scope: 1, frame: 1}, ctx: EvalContext{evalStatement: evalStatement{Context: context.Background()}}}
}
func (f insertConflictProofFixture) comparison(t *testing.T, op string, left, right expr.Expr) comparisonExpression {
	t.Helper()
	operators := map[string]sqltype.ComparisonOperator{"<": sqltype.CompareLess, "<=": sqltype.CompareLessEqual, "=": sqltype.CompareEqual, ">=": sqltype.CompareGreaterEqual, ">": sqltype.CompareGreater, "<>": sqltype.CompareNotEqual}
	predicate, err := sqltype.BindComparisonPredicate(operators[op], left.ResultUsage().Type, right.ResultUsage().Type, f.boolean, left.ResultUsage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	return comparisonExpression{Info: expr.Info{Usage: f.boolean.Usage(), Property: expr.Properties{RowDependent: left.Properties().RowDependent || right.Properties().RowDependent, ParameterDependent: left.Properties().ParameterDependent || right.Properties().ParameterDependent}}, left: left, right: right, predicate: predicate, operator: op}
}
func (f insertConflictProofFixture) integerLiteral(t *testing.T, n int64) literalExpression {
	t.Helper()
	value, err := sqltype.Integer(f.integer, n)
	if err != nil {
		t.Fatal(err)
	}
	return literalExpression{Info: expr.Info{Usage: f.integer.Usage()}, value: value}
}
func TestInsertConflictPredicateConstantMatrix(t *testing.T) {
	f := insertConflictProofTypes(t)
	operators := []string{"<", "<=", "=", ">=", ">", "<>"}
	// Independent released implications: columns < <= = >= > <>. These are
	// expectations, not references to the implementation's decision tables.
	for _, c := range []struct {
		constant int64
		want     [6]string
	}{
		{7, [6]string{"TTFFFT", "TTFFFT", "TTFFFT", "FFFFFF", "FFFFFF", "FFFFFF"}},
		{5, [6]string{"TTFFFT", "FTFFFF", "FTTTFF", "FFFTFF", "FFFTTT", "FFFFFT"}},
		{3, [6]string{"FFFFFF", "FFFFFF", "FFFTTT", "FFFTTT", "FFFTTT", "FFFFFF"}},
	} {
		for i, aop := range operators {
			for j, bop := range operators {
				a := f.comparison(t, aop, f.column, f.integerLiteral(t, 5))
				b := f.comparison(t, bop, f.column, f.integerLiteral(t, c.constant))
				got, err := conflictPredicateImpliedBy(&f.ctx, a, b)
				want := c.want[i][j] == 'T'
				if err != nil || got != want {
					t.Fatalf("x%s5 => x%s%d: %t %v want %t", aop, bop, c.constant, got, err, want)
				}
			}
		}
	}
}
func TestInsertConflictPredicateCompositionAndNull(t *testing.T) {
	f := insertConflictProofTypes(t)
	positive := f.comparison(t, ">", f.column, f.integerLiteral(t, 0))
	stronger := f.comparison(t, ">", f.column, f.integerLiteral(t, 10))
	weaker := f.comparison(t, ">=", f.column, f.integerLiteral(t, 0))
	boolean := columnExpression{Info: expr.Info{Usage: f.boolean.Usage(), Property: expr.Properties{RowDependent: true}}, scope: 1, frame: 1, slot: 1}
	logical := func(op parser.LogicalOperator, a, b expr.Expr) logicalExpression {
		return logicalExpression{Info: expr.Info{Usage: f.boolean.Usage(), Property: expr.Properties{RowDependent: true}}, op: op, left: a, right: b}
	}
	nullRequired := nullTestExpression{Info: expr.Info{Usage: f.boolean.Usage(), Property: expr.Properties{RowDependent: true}}, child: f.column, not: true}
	isNotTrue := truthTestExpression{Info: expr.Info{Usage: f.boolean.Usage(), Property: expr.Properties{RowDependent: true}}, child: boolean, truth: True, not: true}
	negated := logical(parser.LogicalNot, boolean, nil)
	truthLiteral := func(truth Truth) literalExpression {
		v := sqltype.Boolean(f.boolean, truth == True)
		if truth == Unknown {
			v = sqltype.Null(f.boolean)
		}
		return literalExpression{Info: expr.Info{Usage: f.boolean.Usage()}, value: v}
	}
	for _, c := range []struct {
		a, b expr.Expr
		want bool
	}{
		{stronger, positive, true}, {weaker, positive, false}, {positive, nullRequired, true},
		{logical(parser.LogicalAnd, stronger, boolean), logical(parser.LogicalAnd, positive, boolean), true},
		{boolean, logical(parser.LogicalOr, boolean, positive), true},
		{logical(parser.LogicalOr, boolean, stronger), positive, false},
		{logical(parser.LogicalOr, stronger, positive), positive, true},
		{logical(parser.LogicalAnd, stronger, boolean), logical(parser.LogicalOr, positive, weaker), true},
		{f.comparison(t, "=", boolean, truthLiteral(True)), boolean, true},
		{f.comparison(t, "=", boolean, truthLiteral(False)), negated, true},
		{isNotTrue, negated, false}, {truthLiteral(Unknown), positive, true}, {truthLiteral(True), positive, false},
	} {
		got, err := conflictPredicateImpliedBy(&f.ctx, c.a, c.b)
		if err != nil || got != c.want {
			t.Fatalf("%T => %T: got %t %v want %t", c.a, c.b, got, err, c.want)
		}
	}
	reverse := f.comparison(t, "<", f.integerLiteral(t, 10), f.column)
	got, err := conflictPredicateImpliedBy(&f.ctx, reverse, positive)
	if err != nil || !got {
		t.Fatal("commutation", got, err)
	}
	other := f.column
	other.slot = 1
	a := f.comparison(t, ">", f.column, other)
	b := f.comparison(t, "<", other, f.column)
	got, err = conflictPredicateImpliedBy(&f.ctx, a, b)
	if err != nil || !got {
		t.Fatal("reverse columns", got, err)
	}
}
func TestInsertConflictPredicateCurrentParameters(t *testing.T) {
	f := insertConflictProofTypes(t)
	parameter := parameterExpression{Info: expr.Info{Usage: f.integer.Usage(), Property: expr.Properties{ParameterDependent: true}}, number: 1}
	target := f.comparison(t, ">", f.column, parameter)
	installed := f.comparison(t, ">", f.column, f.integerLiteral(t, 0))
	for _, c := range []struct {
		n    int64
		want bool
	}{{10, true}, {-1, false}, {0, true}} {
		value := f.integerLiteral(t, c.n).value
		f.ctx.Parameters = []sqltype.Value{value}
		got, err := conflictPredicateImpliedBy(&f.ctx, target, installed)
		if err != nil || got != c.want {
			t.Fatal(c.n, got, err)
		}
	}
	// A previous specialization never mutates the retained analyzed parameter.
	if _, ok := target.right.(parameterExpression); !ok {
		t.Fatal("retained predicate was specialized")
	}
}
func TestInsertConflictPredicateNegativeProviderProof(t *testing.T) {
	f := insertConflictProofTypes(t)
	e := New()
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	literal := func(input string) literalExpression {
		value, err := text.Input(context.Background(), input, -1)
		if err != nil {
			t.Fatal(err)
		}
		return literalExpression{Info: expr.Info{Usage: text.Usage()}, value: value}
	}
	// Analysis properties are not executable volatility: a comparison between
	// an ordinary column and a literal stays immutable even if manually mislabeled.
	// FORMAT is an actual surviving stable operation with a row-dependent argument.
	stable := functions.Format{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{RowDependent: true, Volatility: expr.Stable}}, Args: []expr.Expr{literal("%s"), f.column}, Int4: f.integer}
	if plannedVolatility(stable) != expr.Stable {
		t.Fatal("fixture has no actual stable operation")
	}
	a := f.comparison(t, ">", stable, literal("10"))
	b := f.comparison(t, ">", stable, literal("0"))
	got, err := conflictPredicateImpliedBy(&f.ctx, a, b)
	if err != nil || got {
		t.Fatal("stable proof", got, err)
	}
	a = f.comparison(t, ">", f.column, f.integerLiteral(t, 10))
	b = f.comparison(t, ">", f.column, f.integerLiteral(t, 0))
	different := f.column
	different.scope = 2
	b.left = different
	got, err = conflictPredicateImpliedBy(&f.ctx, a, b)
	if err != nil || got {
		t.Fatal("foreign namespace proof", got, err)
	}
}
func TestInsertConflictPredicateTypedProviders(t *testing.T) {
	f := insertConflictProofTypes(t)
	e := New()
	literal := func(id sqltype.ID, text string) literalExpression {
		descriptor, _ := e.CatalogRead().LookupType(id)
		value, err := descriptor.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return literalExpression{Info: expr.Info{Usage: descriptor.Usage()}, value: value}
	}
	for _, c := range []struct {
		id        sqltype.ID
		high, low string
	}{{sqltype.Numeric, "10.000", "0.00"}, {sqltype.Float8, "NaN", "1"}, {sqltype.Date, "2001-01-01", "2000-01-01"}} {
		high, low := literal(c.id, c.high), literal(c.id, c.low)
		column := columnExpression{Info: expr.Info{Usage: high.Usage, Property: expr.Properties{RowDependent: true}}, scope: 1, frame: 1}
		a := f.comparison(t, "=", column, high)
		b := f.comparison(t, ">", column, low)
		got, err := conflictPredicateImpliedBy(&f.ctx, a, b)
		if err != nil || !got {
			t.Fatal(c.id, got, err)
		}
	}
	// Concrete comparisons exist, but not every cross-input pair belongs to the
	// same B-tree family; no inferred order theorem crosses that boundary.
	a := f.comparison(t, ">", f.column, literal(sqltype.Numeric, "10"))
	b := f.comparison(t, ">", f.column, f.integerLiteral(t, 0))
	got, err := conflictPredicateImpliedBy(&f.ctx, a, b)
	if err != nil || got {
		t.Fatal("cross-family theorem", got, err)
	}
	stamp := literal(sqltype.Timestamp, "2001-01-01 00:00:00")
	zoned := literal(sqltype.Timestamptz, "2000-01-01 00:00:00+00")
	column := columnExpression{Info: expr.Info{Usage: stamp.Usage, Property: expr.Properties{RowDependent: true}}, scope: 1, frame: 1}
	a = f.comparison(t, ">", column, zoned)
	b = f.comparison(t, ">", column, literal(sqltype.Timestamp, "1999-01-01 00:00:00"))
	if !a.predicate.SessionDependent() {
		t.Fatal("fixture is not an actual session-dependent provider")
	}
	got, err = conflictPredicateImpliedBy(&f.ctx, a, b)
	if err != nil || got {
		t.Fatal("session-dependent theorem", got, err)
	}
	text := literal(sqltype.Text, "z")
	otherText := literal(sqltype.Text, "a")
	left := columnExpression{Info: expr.Info{Usage: text.Usage, Property: expr.Properties{RowDependent: true}}, scope: 1, frame: 1}
	right := left
	// Use owned collations, not an invented comparator/provider.
	right.Usage.Collation = sqltype.CollationRef{ID: 950, Derivation: sqltype.Default}
	a = f.comparison(t, ">", left, text)
	b = f.comparison(t, ">", right, otherText)
	got, err = conflictPredicateImpliedBy(&f.ctx, a, b)
	if err != nil || got {
		t.Fatal("different collation theorem", got, err)
	}
	unrelated := f.column
	unrelated.slot = 9
	a = f.comparison(t, ">", f.column, f.integerLiteral(t, 10))
	b = f.comparison(t, ">", unrelated, f.integerLiteral(t, 0))
	got, err = conflictPredicateImpliedBy(&f.ctx, a, b)
	if err != nil || got {
		t.Fatal("unrelated column theorem", got, err)
	}
}
