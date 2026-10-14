package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDatetimeBinBinding(t *testing.T) {
	utc, _ := sqltype.ResolveZone("UTC")
	for _, tc := range []struct {
		source, origin string
		result         sqltype.ID
		volatility     Volatility
	}{
		{"timestamp", "timestamp", sqltype.Timestamp, Immutable},
		{"timestamptz", "timestamptz", sqltype.Timestamptz, Immutable},
		{"timestamp", "timestamptz", sqltype.Timestamptz, Stable},
		{"timestamptz", "timestamp", sqltype.Timestamptz, Stable},
		{"date", "timestamp", sqltype.Timestamp, Immutable},
		{"timestamp", "date", sqltype.Timestamp, Immutable},
		{"date", "timestamptz", sqltype.Timestamptz, Stable},
		{"timestamptz", "date", sqltype.Timestamptz, Stable},
		{"date", "date", sqltype.Timestamptz, Stable},
		{"", "", sqltype.Timestamptz, Immutable},
		{"timestamp", "", sqltype.Timestamp, Immutable},
		{"", "timestamp", sqltype.Timestamp, Immutable},
		{"timestamptz", "", sqltype.Timestamptz, Immutable},
		{"", "timestamptz", sqltype.Timestamptz, Immutable},
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{Zone: utc}, nil)
		arg := func(typ, value string) parser.Expr {
			if typ == "" {
				return datetimeMakeString(value)
			}
			return ageFiniteTyped(typ, value)
		}
		node, err := b.bindDatetimeBin(datetimeMakeRaw("date_bin", datetimeMakeString("1 day"), arg(tc.source, "2000-01-02"), arg(tc.origin, "2000-01-01")), true)
		if err != nil {
			t.Fatal(tc, err)
		}
		if node.ResultUsage().Type.ID() != tc.result || node.ResultUsage().Typmod != -1 || node.Properties().Volatility != tc.volatility || plannedVolatility(node) != tc.volatility || generationImmutable(node) != (tc.volatility == Immutable) {
			t.Fatal(tc, node.ResultUsage(), node.Properties())
		}
		v, err := node.Eval(&EvalContext{Context: context.Background(), Input: sqltype.InputEnvironment{Zone: utc}})
		n, failure := v.Timestamp()
		if err != nil || failure != nil || n != 86400000000 {
			t.Fatal(tc, v, err, failure)
		}
	}
	for _, args := range [][]parser.Expr{
		{}, {datetimeMakeString("1 day")},
		{datetimeMakeString("1 day"), ageFiniteTyped("timestamp", "2000-01-02"), datetimeMakeInteger(1)},
		{ageFiniteTyped("text", "1 day"), ageFiniteTyped("timestamp", "2000-01-02"), ageFiniteTyped("timestamp", "2000-01-01")},
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{Zone: utc}, nil)
		_, err := b.bindDatetimeBin(datetimeMakeRaw("date_bin", args...), true)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42883" || d.Position != "8" || d.Hint != "No function matches the given name and argument types. You might need to add explicit type casts." {
			t.Fatal(err)
		}
	}
}

func TestDatetimeBinPropertiesAndRetag(t *testing.T) {
	utc, _ := sqltype.ResolveZone("UTC")
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{Zone: utc}, nil)
	node, err := b.bindDatetimeBin(datetimeMakeRaw("date_bin", parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2}, parser.ParameterExpr{Number: 3}), true)
	if err != nil {
		t.Fatal(err)
	}
	if b.parameters[1].Type.ID() != sqltype.Interval || b.parameters[2].Type.ID() != sqltype.Timestamptz || b.parameters[3].Type.ID() != sqltype.Timestamptz || !node.Properties().ParameterDependent || node.Properties().Volatility != Immutable {
		t.Fatal(node.Properties(), b.parameters)
	}
	for _, id := range []string{"timestamp", "timestamptz"} {
		node, err := b.bindDatetimeBin(datetimeMakeRaw("date_bin", datetimeMakeString("15 minutes"), ageFiniteTyped(id, "2000-01-01 00:02:29.999999"), ageFiniteTyped(id, "2000-01-01 00:02:30")), true)
		if err != nil {
			t.Fatal(err)
		}
		e := node.(functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties])
		replacements := map[sqltype.ID]*sqltype.Descriptor{}
		for _, d := range sqltype.Builtins() {
			replacements[d.ID()] = d
		}
		next, changed, err := retagBoundTypes(New().CatalogRead(), e, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
		if err != nil || !changed {
			t.Fatal(next, changed, err)
		}
		rebound := next.(functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties])
		for i, arg := range rebound.Args {
			if arg.ResultUsage().Type == e.Args[i].ResultUsage().Type {
				t.Fatal("retained child authority")
			}
		}
		v, err := rebound.Eval(&EvalContext{Context: context.Background(), Input: sqltype.InputEnvironment{Zone: utc}})
		if err != nil || v.Type() != replacements[e.Usage.Type.ID()] {
			t.Fatal(v, err)
		}
		number, failure := v.Timestamp()
		if failure != nil || number != -750000000 {
			t.Fatal(id, v, failure)
		}
		for i := range e.Args {
			null := e
			null.Args[i] = literalExpression{expressionInfo: b.info(e.Args[i].ResultUsage(), parser.Position{}, ExprProperties{Volatility: Immutable}), value: sqltype.Null(e.Args[i].ResultUsage().Type)}
			simplified, changed, err := simplifyConditional(&EvalContext{Context: context.Background()}, null, false)
			if err != nil || !changed || !generationImmutable(simplified) {
				t.Fatal(simplified, changed, err)
			}
		}
	}
	flags := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{Zone: utc}, nil)
	flags.relation = &relationDescriptor{columns: []ColumnDescriptor{{Name: "z", Usage: flags.typ(sqltype.Timestamptz).Usage()}}, attributes: []int32{1}, index: map[string]int{"z": 0}}
	column := parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "z"}}}}
	volatile, err := flags.bindDatetimeBin(datetimeMakeRaw("date_bin", parser.ParameterExpr{Number: 1}, column, datetimeMakeRaw("clock_timestamp")), true)
	if err != nil {
		t.Fatal(err)
	}
	p := volatile.Properties()
	if !p.RowDependent || !p.ParameterDependent || p.StageDependent || p.Volatility != Volatile || plannedVolatility(volatile) != Volatile || generationImmutable(volatile) {
		t.Fatal(p)
	}
	flags.aggregateClause = "SELECT"
	stage, err := flags.bind(datetimeMakeRaw("date_bin", parser.ParameterExpr{Number: 1}, parser.AggregateExpr{Name: "min", Args: []parser.Expr{column}}, column), expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	p = stage.Properties()
	if !p.StageDependent || !p.RowDependent || !p.ParameterDependent || p.Volatility != Immutable {
		t.Fatal(p)
	}
}

func TestDatetimeBinStoredTraversal(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	node, err := b.bindDatetimeBin(datetimeMakeRaw("date_bin", datetimeMakeString("15 minutes"), ageFiniteTyped("timestamp", "2000-01-02"), ageFiniteTyped("timestamp", "2000-01-01")), true)
	if err != nil {
		t.Fatal(err)
	}
	e := node.(functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties])
	column := columnExpression{expressionInfo: b.info(b.typ(sqltype.Timestamp).Usage(), parser.Position{}, ExprProperties{RowDependent: true}), origin: ObjectRef{Address: ObjectAddress{ClassID: RelationClassID, ObjectID: 123, SubID: 1}}, slot: 0}
	e.Args[1] = column
	e.Property.RowDependent = true
	var children []BoundExpr
	boundExpressionChildren(e, func(arg BoundExpr) { children = append(children, arg) })
	if len(children) != 3 || children[1] != column || !b.sameGroupExpression(e, e) {
		t.Fatal(children)
	}
	different := e
	different.Args[1] = e.Args[2]
	if b.sameGroupExpression(e, different) {
		t.Fatal("distinct children treated as equal")
	}
	rewritten, err := b.rewriteGroup(e, []BoundExpr{column})
	if err != nil || !rewritten.Properties().StageDependent {
		t.Fatal(rewritten, err)
	}
	if _, ok := rewritten.(functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties]).Args[1].(groupExpression); !ok {
		t.Fatal("group child not rewritten")
	}
	mapped, err := remapBoundExpression(e, func(ref ObjectRef) ObjectRef { ref.Address.ObjectID = 456; return ref }, map[int32]int{1: 2})
	if err != nil {
		t.Fatal(err)
	}
	remapped := mapped.(functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties]).Args[1].(columnExpression)
	if remapped.origin.Address.ObjectID != 456 || remapped.slot != 2 {
		t.Fatal(remapped)
	}
	// Preflight must traverse a child codec conversion rather than attempting
	// date_bin on a mismatched descriptor or bypassing coercion failures.
	text := literalExpression{expressionInfo: b.info(b.typ(sqltype.Text).Usage(), parser.Position{}, ExprProperties{Volatility: Immutable}), value: sqltype.String(b.typ(sqltype.Text), "not-a-timestamp")}
	plan, err := sqltype.PlanCoercion(text.ResultUsage(), b.typ(sqltype.Timestamp).Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	e.Args[1] = coercionExpression{b.info(b.typ(sqltype.Timestamp).Usage(), parser.Position{}, ExprProperties{Volatility: Immutable}), text, plan, sqltype.Explicit, false}
	_, _, err = preflightCoercions(&EvalContext{Context: context.Background()}, e)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "22007" {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Eval(&EvalContext{Context: ctx}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
