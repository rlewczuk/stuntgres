package dml

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

type insertConflictProofFixture struct {
	integer, boolean *sqltype.Descriptor
	column           plan.ColumnExpr
	ctx              exec.Context
}

func insertConflictProofTypes(t *testing.T) insertConflictProofFixture {
	t.Helper()
	integer, _ := builtinType(sqltype.Int4)
	boolean, _ := builtinType(sqltype.Bool)
	state := catalog.NewState(1)
	catalog.InstallBuiltinFamilies(state, authorization.PrincipalRef{})
	catalog.InstallBuiltinClasses(state, authorization.PrincipalRef{})
	procedures := routine.BuiltinProcedures(authorization.PrincipalRef{})
	for i := range procedures {
		state.SetDescriptor(procedures[i].Address(), &procedures[i])
	}
	return insertConflictProofFixture{integer: integer, boolean: boolean, column: plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1}, ctx: exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: catalog.NewRead(nil, state)}}}
}
func (f insertConflictProofFixture) comparison(t *testing.T, op string, left, right expr.Expr) plan.ComparisonExpr {
	t.Helper()
	operators := map[string]sqltype.ComparisonOperator{"<": sqltype.CompareLess, "<=": sqltype.CompareLessEqual, "=": sqltype.CompareEqual, ">=": sqltype.CompareGreaterEqual, ">": sqltype.CompareGreater, "<>": sqltype.CompareNotEqual}
	predicate, err := sqltype.BindComparisonPredicate(operators[op], left.ResultUsage().Type, right.ResultUsage().Type, f.boolean, left.ResultUsage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	return plan.ComparisonExpr{Info: expr.Info{Usage: f.boolean.Usage(), Property: expr.Properties{RowDependent: left.Properties().RowDependent || right.Properties().RowDependent, ParameterDependent: left.Properties().ParameterDependent || right.Properties().ParameterDependent}}, Left: left, Right: right, Predicate: predicate, Operator: op}
}
func (f insertConflictProofFixture) integerLiteral(t *testing.T, n int64) plan.LiteralExpr {
	t.Helper()
	value, err := sqltype.Integer(f.integer, n)
	if err != nil {
		t.Fatal(err)
	}
	return plan.LiteralExpr{Info: expr.Info{Usage: f.integer.Usage()}, Value: value}
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
	boolean := plan.ColumnExpr{Info: expr.Info{Usage: f.boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 1}
	logical := func(op parser.LogicalOperator, a, b expr.Expr) plan.LogicalExpr {
		return plan.LogicalExpr{Info: expr.Info{Usage: f.boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Op: op, Left: a, Right: b}
	}
	nullRequired := plan.NullTestExpr{Info: expr.Info{Usage: f.boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Child: f.column, Not: true}
	isNotTrue := plan.TruthTestExpr{Info: expr.Info{Usage: f.boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Child: boolean, Truth: plan.True, Not: true}
	negated := logical(parser.LogicalNot, boolean, nil)
	truthLiteral := func(truth plan.Truth) plan.LiteralExpr {
		v := sqltype.Boolean(f.boolean, truth == plan.True)
		if truth == plan.Unknown {
			v = sqltype.Null(f.boolean)
		}
		return plan.LiteralExpr{Info: expr.Info{Usage: f.boolean.Usage()}, Value: v}
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
		{f.comparison(t, "=", boolean, truthLiteral(plan.True)), boolean, true},
		{f.comparison(t, "=", boolean, truthLiteral(plan.False)), negated, true},
		{isNotTrue, negated, false}, {truthLiteral(plan.Unknown), positive, true}, {truthLiteral(plan.True), positive, false},
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
	other.Slot = 1
	a := f.comparison(t, ">", f.column, other)
	b := f.comparison(t, "<", other, f.column)
	got, err = conflictPredicateImpliedBy(&f.ctx, a, b)
	if err != nil || !got {
		t.Fatal("reverse columns", got, err)
	}
}
func TestInsertConflictPredicateCurrentParameters(t *testing.T) {
	f := insertConflictProofTypes(t)
	parameter := plan.ParameterExpr{Info: expr.Info{Usage: f.integer.Usage(), Property: expr.Properties{ParameterDependent: true}}, Number: 1}
	target := f.comparison(t, ">", f.column, parameter)
	installed := f.comparison(t, ">", f.column, f.integerLiteral(t, 0))
	for _, c := range []struct {
		n    int64
		want bool
	}{{10, true}, {-1, false}, {0, true}} {
		value := f.integerLiteral(t, c.n).Value
		f.ctx.Parameters = []sqltype.Value{value}
		got, err := conflictPredicateImpliedBy(&f.ctx, target, installed)
		if err != nil || got != c.want {
			t.Fatal(c.n, got, err)
		}
	}
	// A previous specialization never mutates the retained analyzed parameter.
	if _, ok := target.Right.(plan.ParameterExpr); !ok {
		t.Fatal("retained predicate was specialized")
	}
}
func TestInsertConflictPredicateNegativeProviderProof(t *testing.T) {
	f := insertConflictProofTypes(t)
	text, _ := builtinType(sqltype.Text)
	literal := func(input string) plan.LiteralExpr {
		value, err := text.Input(context.Background(), input, -1)
		if err != nil {
			t.Fatal(err)
		}
		return plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: value}
	}
	// Analysis properties are not executable volatility: a comparison between
	// an ordinary column and a literal stays immutable even if manually mislabeled.
	// FORMAT is an actual surviving stable operation with a row-dependent argument.
	stable := functions.Format{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{RowDependent: true, Volatility: expr.Stable}}, Args: []expr.Expr{literal("%s"), f.column}, Int4: f.integer}
	if exec.PlannedVolatility(stable) != expr.Stable {
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
	different.Scope = 2
	b.Left = different
	got, err = conflictPredicateImpliedBy(&f.ctx, a, b)
	if err != nil || got {
		t.Fatal("foreign namespace proof", got, err)
	}
}
func TestInsertConflictPredicateTypedProviders(t *testing.T) {
	f := insertConflictProofTypes(t)
	literal := func(id sqltype.ID, text string) plan.LiteralExpr {
		descriptor, _ := builtinType(id)
		value, err := descriptor.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return plan.LiteralExpr{Info: expr.Info{Usage: descriptor.Usage()}, Value: value}
	}
	for _, c := range []struct {
		id        sqltype.ID
		high, low string
	}{{sqltype.Numeric, "10.000", "0.00"}, {sqltype.Float8, "NaN", "1"}, {sqltype.Date, "2001-01-01", "2000-01-01"}} {
		high, low := literal(c.id, c.high), literal(c.id, c.low)
		column := plan.ColumnExpr{Info: expr.Info{Usage: high.Usage, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1}
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
	column := plan.ColumnExpr{Info: expr.Info{Usage: stamp.Usage, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1}
	a = f.comparison(t, ">", column, zoned)
	b = f.comparison(t, ">", column, literal(sqltype.Timestamp, "1999-01-01 00:00:00"))
	if !a.Predicate.SessionDependent() {
		t.Fatal("fixture is not an actual session-dependent provider")
	}
	got, err = conflictPredicateImpliedBy(&f.ctx, a, b)
	if err != nil || got {
		t.Fatal("session-dependent theorem", got, err)
	}
	text := literal(sqltype.Text, "z")
	otherText := literal(sqltype.Text, "a")
	left := plan.ColumnExpr{Info: expr.Info{Usage: text.Usage, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1}
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
	unrelated.Slot = 9
	a = f.comparison(t, ">", f.column, f.integerLiteral(t, 10))
	b = f.comparison(t, ">", unrelated, f.integerLiteral(t, 0))
	got, err = conflictPredicateImpliedBy(&f.ctx, a, b)
	if err != nil || got {
		t.Fatal("unrelated column theorem", got, err)
	}
}

// builtinType finds a builtin type descriptor, as the engine catalog seeds it.
func builtinType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	for _, d := range sqltype.Builtins() {
		if d.ID() == id {
			return d, true
		}
	}
	return nil, false
}
