package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDatetimeOverlapsBindingAndRetag(t *testing.T) {
	zone, err := sqltype.ResolveZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{Zone: zone}, nil)
	for _, typ := range []string{"time", "timestamp", "timestamptz", "timetz"} {
		value := "2001-01-01"
		if typ == "time" {
			value = "01:00"
		}
		if typ == "timetz" {
			value = "01:00+00"
		}
		if typ == "timestamptz" {
			value = "2001-01-01+00"
		}
		count := 4
		if typ == "timetz" {
			count = 1
		}
		for mask := range count {
			args := [4]parser.Expr{ageFiniteTyped(typ, value), ageFiniteTyped(typ, value), ageFiniteTyped(typ, value), ageFiniteTyped(typ, value)}
			if mask&1 != 0 {
				args[1] = ageFiniteTyped("interval", "1 hour")
			}
			if mask&2 != 0 {
				args[3] = ageFiniteTyped("interval", "1 hour")
			}
			node, err := b.bindDatetimeOverlaps(datetimeMakeRaw("overlaps", args[:]...), true)
			if err != nil {
				t.Fatal(typ, mask, err)
			}
			e := node.(functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties])
			stable := typ == "timestamptz" && mask != 0
			if e.ResultUsage().Type.ID() != sqltype.Bool || e.ResultUsage().Typmod != -1 || (e.Properties().Volatility == Stable) != stable || (plannedVolatility(e) == Stable) != stable || generationImmutable(e) != !stable {
				t.Fatal(typ, mask, e.Properties())
			}
			if !b.sameGroupExpression(e, e) {
				t.Fatal("structural equality")
			}
			count := 0
			boundExpressionChildren(e, func(BoundExpr) { count++ })
			if count != 4 {
				t.Fatal("lexical walker", count)
			}
			replacements := map[sqltype.ID]*sqltype.Descriptor{}
			for _, d := range sqltype.Builtins() {
				replacements[d.ID()] = d
			}
			rebound, changed, err := retagBoundTypes(New().CatalogRead(), e, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
			if err != nil || !changed {
				t.Fatal(rebound, changed, err)
			}
			next := rebound.(functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties])
			if next.ResultUsage().Type != replacements[sqltype.Bool] || next.Plan == e.Plan {
				t.Fatal("retained plan authority")
			}
			for i, arg := range next.Args {
				if arg.ResultUsage().Type == e.Args[i].ResultUsage().Type {
					t.Fatal("retained child authority")
				}
			}
			if _, err := next.Eval(&EvalContext{Context: context.Background(), Input: sqltype.InputEnvironment{Zone: zone}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		raw      parser.FunctionExpr
		endpoint sqltype.ID
		duration bool
	}{
		{datetimeMakeRaw("overlaps", ageFiniteTyped("date", "2001-01-01"), ageFiniteTyped("date", "2001-01-03"), ageFiniteTyped("date", "2001-01-02"), ageFiniteTyped("date", "2001-01-04")), sqltype.Timestamptz, false},
		{datetimeMakeRaw("overlaps", ageFiniteTyped("timestamp", "2001-01-01"), datetimeMakeString("2001-01-03"), ageFiniteTyped("timestamp", "2001-01-02"), datetimeMakeString("2001-01-04")), sqltype.Timestamp, false},
		{datetimeMakeRaw("overlaps", ageFiniteTyped("timestamp", "2001-01-01"), ageFiniteTyped("time", "24:00"), ageFiniteTyped("timestamp", "2001-01-01 12:00"), ageFiniteTyped("timestamp", "2001-01-01 12:00")), sqltype.Timestamp, true},
	} {
		node, err := b.bindDatetimeOverlaps(tc.raw, true)
		if err != nil {
			t.Fatal(err)
		}
		e := node.(functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties])
		if e.Plan.ArgumentType(0).ID() != tc.endpoint || e.Plan.Duration()[0] != tc.duration {
			t.Fatal(e.Plan)
		}
	}
}

func TestDatetimeOverlapsUnknownAndParameterOccurrences(t *testing.T) {
	for _, raw := range []parser.FunctionExpr{
		datetimeMakeRaw("overlaps", datetimeMakeString("a"), datetimeMakeString("b"), datetimeMakeString("c"), datetimeMakeString("d")),
		datetimeMakeRaw("overlaps", ageFiniteTyped("time", "01:00"), datetimeMakeString("03:00"), ageFiniteTyped("time", "02:00"), datetimeMakeString("04:00")),
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		_, err := b.bindDatetimeOverlaps(raw, true)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42725" {
			t.Fatal(err)
		}
	}
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	raw := datetimeMakeRaw("overlaps", parser.ParameterExpr{Number: 1}, ageFiniteTyped("timestamp", "2001-01-03"), parser.ParameterExpr{Number: 2}, ageFiniteTyped("timestamp", "2001-01-04"))
	node, err := b.bindDatetimeOverlaps(raw, true)
	if err != nil || !node.Properties().ParameterDependent || b.parameters[1].Type.ID() != sqltype.Timestamp || b.parameters[2].Type.ID() != sqltype.Timestamp {
		t.Fatal(node, err, b.parameters)
	}
	b = newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	raw = datetimeMakeRaw("overlaps", parser.ParameterExpr{Number: 1, Pos: parser.Position{Character: 9}}, parser.CastExpr{Child: parser.ParameterExpr{Number: 1}, TypeName: parser.TypeName{Name: "interval"}}, ageFiniteTyped("timestamp", "2001-01-02"), ageFiniteTyped("interval", "1 day"))
	_, err = b.bindDatetimeOverlaps(raw, true)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "42P08" || d.Detail != "interval versus timestamp without time zone" || d.Position != "9" {
		t.Fatal(err)
	}
}

func TestDatetimeOverlapsInlineSafetyAndContext(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	node, err := b.bindDatetimeOverlaps(datetimeMakeRaw("overlaps", ageFiniteTyped("timestamp", "294276-12-31"), ageFiniteTyped("interval", "1 day"), ageFiniteTyped("timestamp", "2000-01-01"), ageFiniteTyped("timestamp", "2000-01-02")), true)
	if err != nil {
		t.Fatal(err)
	}
	e := node.(functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties])
	c := &EvalContext{Context: context.Background()}
	_, err = e.Eval(c)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Context != `SQL function "overlaps" statement 1` {
		t.Fatal(err)
	}
	// A nonconstant second pair makes a constant first addition fail inlining.
	e.Args[2] = columnExpression{expressionInfo: boundExpressionInfo(e.Args[2])}
	e.Args[3] = columnExpression{expressionInfo: boundExpressionInfo(e.Args[3])}
	_, _, err = simplifyOverlapsRoute(c, e)
	if !errors.As(err, &d) || d.Context != `SQL function "overlaps" during inlining` {
		t.Fatal(err)
	}
	start := columnExpression{expressionInfo: boundExpressionInfo(e.Args[0])}
	e.Args[0] = start
	if !overlapsInlineSafe(e) {
		t.Fatal("cheap variable cannot inline")
	}
	for i := range 11 {
		e.Args[0] = arithmeticExpression{expressionInfo: boundExpressionInfo(start), left: e.Args[0], right: literalExpression{expressionInfo: boundExpressionInfo(e.Args[1])}}
		if overlapsInlineSafe(e) != (i < 10) {
			t.Fatal("function-cost threshold", i)
		}
	}
	e.Args[0] = subqueryExpression{expressionInfo: boundExpressionInfo(start)}
	if overlapsInlineSafe(e) {
		t.Fatal("duplicated subquery inlined")
	}
	order := []int{}
	volatile := mathObservedArgument{expressionInfo: expressionInfo{usage: start.ResultUsage(), properties: ExprProperties{Volatility: Volatile}}, order: &order}
	e.Args[0] = volatile
	if overlapsInlineSafe(e) {
		t.Fatal("duplicated volatile start inlined")
	}
	volatile.usage = e.Plan.ArgumentType(1).Usage()
	e.Args[0] = start
	e.Args[1] = volatile
	if !overlapsInlineSafe(e) {
		t.Fatal("single-use volatile duration prevented inline")
	}
}

func TestDatetimeOverlapsFoldedBodyAndExecuteOwnedParameters(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	raw := datetimeMakeRaw("overlaps", ageFiniteTyped("timestamp", "2000-01-01"), ageFiniteTyped("interval", "1 day"),
		parser.CastExpr{Child: parser.ParameterExpr{Number: 1}, TypeName: parser.TypeName{Name: "timestamp"}},
		parser.CastExpr{Child: parser.ParameterExpr{Number: 2}, TypeName: parser.TypeName{Name: "timestamp"}})
	node, err := b.bindDatetimeOverlaps(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	c := &EvalContext{Context: ctx}
	folded, changed, err := simplifyOverlapsRoute(c, node.(functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties]))
	if err != nil || !changed || folded.Folded[0].Type() != b.typ(sqltype.Timestamp) || folded.Folded[1].Type() != nil {
		t.Fatal(folded, changed, err)
	}
	// Parameters remain Execute-owned; persistent descriptor replacement admits
	// closed stored trees, exercised separately with real scoped column leaves.
	for _, tc := range []struct {
		start, end string
		want       bool
	}{{"2000-01-01", "2000-01-03", true}, {"2000-02-01", "2000-02-02", false}, {"2000-01-01", "2000-01-03", true}} {
		start, err := b.typ(sqltype.Timestamp).Input(ctx, tc.start, -1)
		if err != nil {
			t.Fatal(err)
		}
		end, err := b.typ(sqltype.Timestamp).Input(ctx, tc.end, -1)
		if err != nil {
			t.Fatal(err)
		}
		c.Parameters = []sqltype.Value{start, end}
		got, err := folded.Eval(c)
		truth, failure := got.Boolean()
		if err != nil || failure != nil || truth != tc.want {
			t.Fatal(got, err, failure)
		}
		if folded.Folded[1].Type() != nil {
			t.Fatal("Execute-owned parameters became retained literal endpoints")
		}
	}
	// Current imported values are constant candidates, not retained literal cache entries.
	p := parameterExpression{expressionInfo: expressionInfo{usage: b.typ(sqltype.Timestamp).Usage(), properties: ExprProperties{ParameterDependent: true}}, number: 1}
	if !overlapsExecutionConstant(p, c) || overlapsExecutionConstant(p, &EvalContext{Context: ctx}) {
		t.Fatal("parameter availability classification")
	}
}

func TestDatetimeOverlapsRetainedCachedEndpointRemap(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	node, err := b.bindDatetimeOverlaps(datetimeMakeRaw("overlaps", ageFiniteTyped("timestamp", "2000-01-01"), ageFiniteTyped("interval", "1 day"),
		ageFiniteTyped("timestamp", "2000-01-01"), ageFiniteTyped("timestamp", "2000-01-03")), true)
	if err != nil {
		t.Fatal(err)
	}
	stored := node.(functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties])
	stored.Property = ExprProperties{RowDependent: true}
	for i := range 2 {
		origin := ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 9000, SubID: int32(i + 1)}}
		stored.Args[i+2] = columnExpression{expressionInfo: b.info(b.typ(sqltype.Timestamp).Usage(), parser.Position{}, ExprProperties{RowDependent: true}), scope: 1, frame: 1, slot: i, origin: origin}
	}
	folded, changed, err := simplifyOverlapsRoute(&EvalContext{Context: ctx}, stored)
	if err != nil || !changed || folded.Folded[0].Type() != b.typ(sqltype.Timestamp) || folded.Folded[1].Type() != nil {
		t.Fatal(folded, changed, err)
	}
	replacements := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		replacements[d.ID()] = d
	}
	rebound, changed, err := retagBoundTypes(New().CatalogRead(), folded, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
	if err != nil || !changed {
		t.Fatal(rebound, changed, err)
	}
	retained := rebound.(functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties])
	if retained.Folded[0].Type() != replacements[sqltype.Timestamp] || folded.Folded[0].Type() != b.typ(sqltype.Timestamp) {
		t.Fatal("cached endpoint authority was not independently retagged")
	}
	mapped, err := remapBoundExpression(retained, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, map[int32]int{1: 1, 2: 0})
	if err != nil {
		t.Fatal(err)
	}
	next := mapped.(functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties])
	for i := range 2 {
		column := next.Args[i+2].(columnExpression)
		if column.slot != 1-i || column.origin.Address.ObjectID != 9100 || column.ResultUsage().Type != replacements[sqltype.Timestamp] {
			t.Fatal("retained column remap", column)
		}
		if retained.Args[i+2].(columnExpression).slot != i {
			t.Fatal("source stored tree mutated")
		}
	}
	if next.Folded != retained.Folded {
		t.Fatal("immutable cached endpoint changed during column remap")
	}
	for _, tc := range []struct {
		start, end string
		want       bool
	}{{"2000-01-01", "2000-01-03", true}, {"2000-02-01", "2000-02-02", false}, {"2000-01-01", "2000-01-03", true}} {
		start, err := replacements[sqltype.Timestamp].Input(ctx, tc.start, -1)
		if err != nil {
			t.Fatal(err)
		}
		end, err := replacements[sqltype.Timestamp].Input(ctx, tc.end, -1)
		if err != nil {
			t.Fatal(err)
		}
		c := &EvalContext{Context: ctx, Scope: 1, Frame: 1, Row: []sqltype.Value{end, start}}
		got, err := next.Eval(c)
		truth, failure := got.Boolean()
		if err != nil || failure != nil || truth != tc.want || got.Type() != replacements[sqltype.Bool] {
			t.Fatal("remapped execution", got, err, failure)
		}
		old, err := b.typ(sqltype.Timestamp).Input(ctx, tc.start, -1)
		if err != nil {
			t.Fatal(err)
		}
		c.Row[1] = old
		if _, err := next.Eval(c); err == nil {
			t.Fatal("old descriptor authority accepted in remapped row")
		}
	}
}

func TestDatetimeOverlapsTypedNullFoldedEndpoint(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	node, err := b.bindDatetimeOverlaps(datetimeMakeRaw("overlaps", ageFiniteTyped("timestamp", "2000-01-02"), ageFiniteTyped("interval", "1 day"),
		ageFiniteTyped("timestamp", "2000-01-01"), ageFiniteTyped("timestamp", "2000-01-04")), true)
	if err != nil {
		t.Fatal(err)
	}
	e := node.(functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties])
	e.Args[1] = literalExpression{expressionInfo: boundExpressionInfo(e.Args[1]), value: sqltype.Null(b.typ(sqltype.Interval))}
	e.Args[2] = columnExpression{expressionInfo: boundExpressionInfo(e.Args[2])}
	e.Args[3] = columnExpression{expressionInfo: boundExpressionInfo(e.Args[3])}
	folded, changed, err := simplifyOverlapsRoute(&EvalContext{Context: ctx}, e)
	if err != nil || !changed || folded.Folded[0].Type() != b.typ(sqltype.Timestamp) || !folded.Folded[0].IsNull() {
		t.Fatal("typed NULL is a valid cached endpoint", folded, changed, err)
	}
	again, changed, err := simplifyOverlapsRoute(&EvalContext{Context: ctx}, folded)
	if err != nil || changed || again.Folded != folded.Folded {
		t.Fatal("typed NULL cache was recomputed", changed, err)
	}
}

func TestDatetimeOverlapsStoredRenderProvenance(t *testing.T) {
	args := [4]string{"a", "b", "c", "d"}
	for _, tc := range []struct {
		syntax     parser.FunctionSyntax
		name, want string
	}{
		{parser.SQLFunctionSyntax, "pg_catalog.overlaps", "((a, b) OVERLAPS (c, d))"},
		{parser.FunctionSyntax(0), "pg_catalog.overlaps", "pg_catalog.overlaps(a, b, c, d)"},
		{parser.FunctionSyntax(0), `"overlaps"`, `"overlaps"(a, b, c, d)`},
	} {
		order := []int{}
		got, err := renderOverlapsCall(tc.syntax, args[:], tc.name, func(i int, arg string) (string, error) {
			order = append(order, i)
			return arg, nil
		})
		if err != nil || got != tc.want || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
			t.Fatal(tc, got, order, err)
		}
	}
	calls := 0
	if _, err := renderOverlapsCall(parser.SQLFunctionSyntax, args[:3], "", func(_ int, arg string) (string, error) {
		calls++
		return arg, nil
	}); err == nil || calls != 0 {
		t.Fatal("invalid retained arity evaluated children", calls, err)
	}
}

func TestDatetimeOverlapsNullIfCostBoundary(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	node, err := b.bindDatetimeOverlaps(datetimeMakeRaw("overlaps", ageFiniteTyped("timestamp", "294276-12-31"), ageFiniteTyped("interval", "1 day"), ageFiniteTyped("timestamp", "2000-01-01"), ageFiniteTyped("timestamp", "2000-01-01")), true)
	if err != nil {
		t.Fatal(err)
	}
	e := node.(functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties])
	e.Property = ExprProperties{RowDependent: true}
	info := b.info(b.typ(sqltype.Timestamp).Usage(), parser.Position{}, ExprProperties{RowDependent: true})
	var start BoundExpr = columnExpression{expressionInfo: info, scope: 1, frame: 1, slot: 0}
	sentinel := columnExpression{expressionInfo: info, scope: 1, frame: 1, slot: 1}
	e.Args[2], e.Args[3] = sentinel, sentinel
	comparison, err := sqltype.BindComparison(b.typ(sqltype.Timestamp), b.typ(sqltype.Timestamp), sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	high, err := b.typ(sqltype.Timestamp).Input(ctx, "294276-12-31", -1)
	if err != nil {
		t.Fatal(err)
	}
	low, err := b.typ(sqltype.Timestamp).Input(ctx, "2000-01-01", -1)
	if err != nil {
		t.Fatal(err)
	}
	c := &EvalContext{Context: ctx, Scope: 1, Frame: 1, Row: []sqltype.Value{high, low}}
	for count := 1; count <= 11; count++ {
		start = conditionalExpression{expressionInfo: info, kind: parser.NullIf, args: []BoundExpr{start, sentinel}, comparison: comparison}
		e.Args[0] = start
		if cost := overlapsTreeCost(start); cost != count || overlapsInlineSafe(e) != (count <= 10) {
			t.Fatal("NULLIF operator cost", count, cost)
		}
		if count < 10 {
			continue
		}
		routed, _, err := simplifyOverlapsRoute(c, e)
		if err != nil {
			t.Fatal(err)
		}
		_, err = routed.Eval(c)
		var d *sqlstate.Error
		want := ""
		if count == 11 {
			want = `SQL function "overlaps" statement 1`
		}
		if !errors.As(err, &d) || d.Code != "22008" || d.Message != "timestamp out of range" || d.Context != want {
			t.Fatal("NULLIF boundary evaluation", count, err)
		}
	}
	if overlapsNodeCost(conditionalExpression{expressionInfo: info, kind: parser.Coalesce, args: []BoundExpr{sentinel, sentinel}}) != 0 || overlapsNodeCost(caseExpression{}) != 0 {
		t.Fatal("zero-overhead control cost changed")
	}
}
