package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"sync"
	"testing"
)

func sumUnitProvider(e *Engine, id sqltype.ID) (aggregateProvider, *sqltype.Descriptor) {
	input, _ := e.CatalogRead().LookupType(id)
	switch id {
	case sqltype.Int2, sqltype.Int4:
		result, _ := e.CatalogRead().LookupType(sqltype.Int8)
		return integralSumProvider{input, result.Usage()}, input
	case sqltype.Int8, sqltype.Numeric:
		result, _ := e.CatalogRead().LookupType(sqltype.Numeric)
		return numericSumProvider{input, result.Usage()}, input
	default:
		return floatSumProvider{input, input.Usage()}, input
	}
}

func TestSumProviders(t *testing.T) {
	ctx := context.Background()
	for _, id := range []sqltype.ID{sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8} {
		p, input := sumUnitProvider(New(), id)
		state, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		empty, err := p.Finalize(ctx, state)
		if err != nil || !empty.IsNull() || empty.Type() != p.ResultUsage().Type || p.ResultUsage().Typmod != -1 {
			t.Fatal(empty, err)
		}
		for _, text := range []string{"1", "3"} {
			v, err := input.Input(ctx, text, -1)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Advance(ctx, state, v, sqltype.Value{}); err != nil {
				t.Fatal(err)
			}
		}
		if err := p.Advance(ctx, state, sqltype.Null(input), sqltype.Value{}); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			v, err := p.Finalize(ctx, state)
			if err != nil || v.Type() != p.ResultUsage().Type {
				t.Fatal(v, err)
			}
			text, err := v.Type().Output(ctx, v)
			if err != nil || text != "4" {
				t.Fatal(text, err)
			}
		}
		foreign, _ := New().CatalogRead().LookupType(id)
		wrong, _ := foreign.Input(ctx, "1", -1)
		for _, invalid := range []sqltype.Value{{}, wrong} {
			err := p.Advance(ctx, state, invalid, sqltype.Value{})
			var diagnostic *sqlstate.Error
			if err == nil || errors.As(err, &diagnostic) {
				t.Fatal("ownership error must remain ordinary", err)
			}
		}
		for _, invalid := range []any{nil, struct{}{}, (*integralSumState)(nil), (*sqltype.NumericSumState)(nil), (*floatSumState)(nil)} {
			if p.Advance(ctx, invalid, sqltype.Null(input), sqltype.Value{}) == nil {
				t.Fatal("invalid state accepted")
			}
			if _, err := p.Finalize(ctx, invalid); err == nil {
				t.Fatal("invalid final state accepted")
			}
		}
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, err := p.Init(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				one, err := input.Input(ctx, "1", -1)
				if err != nil {
					t.Error(err)
					return
				}
				if err := p.Advance(ctx, s, one, sqltype.Value{}); err != nil {
					t.Error(err)
					return
				}
				out, err := p.Finalize(ctx, s)
				if err != nil {
					t.Error(err)
					return
				}
				text, err := out.Type().Output(ctx, out)
				if err != nil || text != "1" {
					t.Error("execution state not isolated", text, err)
				}
			}()
		}
		wg.Wait()
	}
}

func TestSumIntegralTransitions(t *testing.T) {
	ctx := context.Background()
	for _, id := range []sqltype.ID{sqltype.Int2, sqltype.Int4} {
		p, input := sumUnitProvider(New(), id)
		state, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		one, _ := sqltype.Integer(input, 1)
		// State boxing and all datum construction happen before allocation measurement.
		if err := p.Advance(ctx, state, one, sqltype.Value{}); err != nil {
			t.Fatal(err)
		}
		if n := testing.AllocsPerRun(100, func() {
			if err := p.Advance(ctx, state, one, sqltype.Value{}); err != nil {
				panic(err)
			}
		}); n != 0 {
			t.Fatal("integral transition allocations", n)
		}
		// Source-derived unchecked transition boundary, not a fabricated SQL overflow.
		s := state.(*integralSumState)
		s.sum = math.MaxInt64
		if err := p.Advance(ctx, state, one, sqltype.Value{}); err != nil || s.sum != math.MinInt64 {
			t.Fatal("released signed64 transition", s.sum, err)
		}
	}
}

func TestSumFloatingTransitions(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		id     sqltype.ID
		values []float64
		want   float64
	}{
		{sqltype.Float4, []float64{16777216, 1, -16777216}, 0},
		{sqltype.Float8, []float64{9007199254740992, 1, -9007199254740992}, 0},
		{sqltype.Float4, []float64{math.SmallestNonzeroFloat32, math.SmallestNonzeroFloat32}, 2 * math.SmallestNonzeroFloat32},
		{sqltype.Float4, []float64{math.Copysign(0, -1), math.Copysign(0, -1)}, math.Copysign(0, -1)},
		{sqltype.Float8, []float64{math.Copysign(0, -1)}, math.Copysign(0, -1)},
		{sqltype.Float4, []float64{math.Inf(1), 1}, math.Inf(1)},
		{sqltype.Float8, []float64{math.Inf(-1), math.Inf(1)}, math.NaN()},
	} {
		p, input := sumUnitProvider(New(), tc.id)
		state, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range tc.values {
			v, err := sqltype.Float(input, n)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Advance(ctx, state, sqltype.Null(input), sqltype.Value{}); err != nil {
				t.Fatal(err)
			}
			if err := p.Advance(ctx, state, v, sqltype.Value{}); err != nil {
				t.Fatal(err)
			}
		}
		v, err := p.Finalize(ctx, state)
		if err != nil {
			t.Fatal(err)
		}
		got, err := v.Float()
		if err != nil || math.IsNaN(tc.want) != math.IsNaN(got) || !math.IsNaN(got) && (got != tc.want || math.Signbit(got) != math.Signbit(tc.want)) {
			t.Fatal(tc, got, err)
		}
	}
	for _, id := range []sqltype.ID{sqltype.Float4, sqltype.Float8} {
		p, input := sumUnitProvider(New(), id)
		state, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		maximum := math.MaxFloat64
		if id == sqltype.Float4 {
			maximum = math.MaxFloat32
		}
		value, _ := sqltype.Float(input, maximum)
		if err := p.Advance(ctx, state, value, sqltype.Value{}); err != nil {
			t.Fatal(err)
		}
		err = p.Advance(ctx, state, value, sqltype.Value{})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22003" || d.Message != "value out of range: overflow" || d.Position != "" {
			t.Fatal("finite transition overflow", err)
		}
	}
}

func TestSumViewProviderRemapping(t *testing.T) {
	source, restored := New(), New()
	ctx := context.Background()
	for _, id := range []sqltype.ID{sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8} {
		provider, input := sumUnitProvider(source, id)
		node := &aggregateNode{child: &singletonNode{}, calls: []aggregateCall{{provider: provider}}}
		mapped, err := remapViewQuery(restored.CatalogRead(), node, func(ref ObjectRef) ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
			typ, _ := restored.CatalogRead().LookupType(old.ID())
			return typ
		})
		if err != nil {
			t.Fatal(err)
		}
		next := mapped.(*aggregateNode).calls[0].provider
		want, restoredInput := sumUnitProvider(restored, id)
		if next.ResultUsage() != want.ResultUsage() || provider.ResultUsage().Type == next.ResultUsage().Type {
			t.Fatal("result descriptor not remapped")
		}
		state, err := next.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		old, _ := input.Input(ctx, "1", -1)
		if next.Advance(ctx, state, old, sqltype.Value{}) == nil {
			t.Fatal("restored provider retained old ownership")
		}
		for _, text := range []string{"1", "3"} {
			v, err := restoredInput.Input(ctx, text, -1)
			if err != nil {
				t.Fatal(err)
			}
			if err := next.Advance(ctx, state, v, sqltype.Value{}); err != nil {
				t.Fatal(err)
			}
		}
		out, err := next.Finalize(ctx, state)
		if err != nil || out.Type() != want.ResultUsage().Type {
			t.Fatal(out, err)
		}
		text, err := out.Type().Output(ctx, out)
		if err != nil || text != "4" {
			t.Fatal(text, err)
		}
		original, err := provider.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := provider.Advance(ctx, original, old, sqltype.Value{}); err != nil {
			t.Fatal("remap mutated original", err)
		}
		newValue, _ := restoredInput.Input(ctx, "1", -1)
		if provider.Advance(ctx, original, newValue, sqltype.Value{}) == nil {
			t.Fatal("original accepted new generation")
		}
	}
}

// Cancellation is triggered on an actual provider context check and propagates
// through a real context's Err/Done; no sleeps or production hooks are needed.
type sumCancelContext struct {
	context.Context
	cancel     context.CancelFunc
	checks, at int
}

func (c *sumCancelContext) Err() error {
	c.checks++
	if c.checks == c.at {
		c.cancel()
	}
	return c.Context.Err()
}
func sumCancellationAt(at int) (*sumCancelContext, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	return &sumCancelContext{Context: ctx, cancel: cancel, at: at}, cancel
}
func TestSumProviderCancellation(t *testing.T) {
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Numeric, sqltype.Float4} {
		p, input := sumUnitProvider(New(), id)
		one, _ := input.Input(context.Background(), "1", -1)
		for _, at := range []int{1, 2} {
			ctx, cancel := sumCancellationAt(at)
			state, err := p.Init(ctx)
			cancel()
			if state != nil || err != context.Canceled {
				t.Fatal("Init cancellation", state, err)
			}
			state, err = p.Init(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel = sumCancellationAt(at)
			err = p.Advance(ctx, state, one, sqltype.Value{})
			cancel()
			if err != context.Canceled {
				t.Fatal("Advance cancellation", err)
			}
			out, err := p.Finalize(context.Background(), state)
			if err != nil || out.IsNull() != (at == 1) {
				t.Fatal("transition boundary", out, err)
			}
			ctx, cancel = sumCancellationAt(at)
			out, err = p.Finalize(ctx, state)
			cancel()
			if out.Type() != nil || err != context.Canceled {
				t.Fatal("Finalize cancellation", out, err)
			}
		}
		fresh, err := p.Init(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Finalize(context.Background(), fresh)
		if err != nil || !out.IsNull() {
			t.Fatal("new execution poisoned", out, err)
		}
	}
}

func TestSumSlotsAndMetadata(t *testing.T) {
	_, q, source := queryCoreFixture(t, "SELECT sum(1),sum(1),sum(1::smallint),sum(1::bigint),sum(1::numeric),sum(1::real),sum(1::double precision),avg(1),count(*) FROM t")
	defer source.close()
	node := q.Root.(*projectNode).child.(*aggregateNode)
	if len(node.calls) != 8 {
		t.Fatal("input-aware immutable sharing", len(node.calls))
	}
	for i, operation := range []aggregateOperation{aggregateIntegralSum, aggregateIntegralSum, aggregateNumericSum, aggregateNumericSum, aggregateFloatSum, aggregateFloatSum, aggregateNumericAvg, aggregateCount} {
		if node.calls[i].operation != operation {
			t.Fatal(i, node.calls[i].operation)
		}
	}
	for i, c := range q.Root.Schema()[:7] {
		want := sqltype.Int8
		switch i {
		case 3, 4:
			want = sqltype.Numeric
		case 5:
			want = sqltype.Float4
		case 6:
			want = sqltype.Float8
		}
		if c.Usage.Type.ID() != want || c.Usage.Typmod != -1 || !c.NullabilityKnown || !c.Nullable || c.OriginTable != 0 || c.OriginAttribute != 0 {
			t.Fatal(c)
		}
	}
}

func TestSumEagerCancellation(t *testing.T) {
	for _, cast := range []string{"integer", "numeric", "real", "double precision"} {
		for _, phase := range []string{"advance", "finalize", "prepublication"} {
			_, q, source := queryCoreFixture(t, "SELECT sum(1::"+cast+") FROM t")
			project := q.Root.(*projectNode)
			node := project.child.(*aggregateNode)
			ctx, cancel := context.WithCancel(context.Background())
			original := node.calls[0].provider
			switch phase {
			case "advance":
				node.calls[0].provider = aggregateProbeProvider{aggregateProvider: original, advance: func(ctx context.Context, s any, v, separator sqltype.Value) error {
					err := original.Advance(ctx, s, v, separator)
					cancel()
					return err
				}}
			case "finalize":
				node.calls[0].provider = aggregateProbeProvider{aggregateProvider: original, finalize: func(ctx context.Context, s any) (sqltype.Value, error) {
					v, err := original.Finalize(ctx, s)
					cancel()
					return v, err
				}}
			case "prepublication":
				expr := project.expressions[0]
				project.expressions[0] = queryProbe(expr.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { v, err := expr.Eval(c); cancel(); return v, err })
			}
			rows, count, err := finalizeQuery(ctx, q.Root, source, nil)
			cancel()
			if rows != nil || count != 0 || err != context.Canceled || source.view != nil || source.anonymous != nil {
				t.Fatal(cast, phase, rows, count, err)
			}
		}
	}
}

func TestSumColumnAuthorization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER sum_reader")
	roleExecute(t, e, op, "CREATE TABLE sum_secret(a INTEGER,b INTEGER,c INTEGER,unused INTEGER)")
	ref := roleTableRef(t, e, "sum_secret")
	owner := op.RoleState().CurrentUser
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	reader, _ := e.Roles().Lookup("sum_reader")
	column := ref
	column.Address.SubID = 1
	entry := authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION sum_reader")
	// Empty input still checks real argument, FILTER, ORDER and hidden reads.
	roleExecute(t, e, op, "SELECT sum(a) FROM sum_secret")
	for _, sql := range []string{
		"SELECT sum(b) FROM sum_secret",
		"SELECT sum(a) FILTER(WHERE b>0) FROM sum_secret",
		"SELECT sum(a ORDER BY c) FROM sum_secret",
		"SELECT 1 FROM sum_secret HAVING sum(b)>0",
		"SELECT sum(a) FROM sum_secret ORDER BY sum(c)",
	} {
		if _, err := e.Describe(op, statement(t, sql)); err != nil {
			t.Fatal("prepare must not cache denial", sql, err)
		}
		roleError(t, e, op, sql, "42501", "permission denied for table sum_secret", "")
	}
	for _, subID := range []int32{2, 3} {
		column.Address.SubID = subID
		if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}); err != nil {
			t.Fatal(err)
		}
	}
	roleExecute(t, e, op, "SELECT sum(a ORDER BY c) FILTER(WHERE b>0) FROM sum_secret")
	roleExecute(t, e, op, "SELECT 1 FROM sum_secret HAVING sum(b)>0 ORDER BY sum(c)")
	// An unrelated column is never demanded by SUM's access manifest.
	roleError(t, e, op, "SELECT sum(unused) FROM sum_secret", "42501", "permission denied for table sum_secret", "")
	column.Address.SubID = 2
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	roleError(t, e, op, "SELECT sum(a) FILTER(WHERE b>0) FROM sum_secret", "42501", "permission denied for table sum_secret", "")
}

func TestSumAnalysisStructure(t *testing.T) {
	sum := parser.AggregateExpr{Name: "sum"}
	one := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}
	for _, tc := range []struct {
		name string
		raw  parser.Expr
		want bool
	}{
		{"nested_cast", parser.CastExpr{Child: parser.CastExpr{Child: sum}}, true},
		{"scalar_composition", parser.OperatorExpr{Args: []parser.Expr{one, parser.FunctionExpr{Args: []parser.Expr{parser.CollateExpr{Child: sum}}}}}, true},
		{"other_aggregate_child", parser.AggregateExpr{Name: "avg", Args: []parser.Expr{sum}}, true},
		{"non_sum_cast", parser.CastExpr{Child: one}, false},
		{"non_sum_aggregate", parser.AggregateExpr{Name: "avg", Args: []parser.Expr{one}}, false},
		{"nil", nil, false},
		{"child_query_only", parser.SubqueryExpr{Query: &parser.Select{Targets: []parser.Expr{sum}}}, false},
		{"wrapped_child_query_only", parser.CastExpr{Child: parser.SubqueryExpr{Query: &parser.Select{Targets: []parser.Expr{sum}}}}, false},
		{"outer_left_sum", parser.SubqueryExpr{Left: parser.CastExpr{Child: sum}, Query: &parser.Select{Targets: []parser.Expr{one}}}, true},
		{"outer_left_non_sum", parser.SubqueryExpr{Left: one, Query: &parser.Select{Targets: []parser.Expr{sum}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsValuesAggregateAnalysis(tc.raw); got != tc.want {
				t.Fatalf("structural VALUES aggregate containment = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSumAnalysisNamespaceRestoration(t *testing.T) {
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	target := &relationDescriptor{name: "sum_unit_target", columns: []ColumnDescriptor{{Name: "i", Usage: integer.Usage()}}}
	one := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Pos: parser.Position{Character: 20}}}
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "sum", Pos: parser.Position{Character: 10}}}}
	for _, existingNamespace := range []bool{false, true} {
		for _, outcome := range []string{"success", "filter_failure", "missing_target_column"} {
			t.Run(map[bool]string{false: "nil_namespace", true: "existing_namespace"}[existingNamespace]+"/"+outcome, func(t *testing.T) {
				b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
				b.aggregateClause, b.insertTarget = "INSERT VALUES", target
				hidden := []queryQualifier{{name: "earlier_hidden", columns: []queryColumn{{descriptor: ColumnDescriptor{Name: "prior", Usage: integer.Usage()}}}}}
				beforeHidden := append([]queryQualifier(nil), hidden...)
				beforeHidden[0].columns = append([]queryColumn(nil), hidden[0].columns...)
				b.queryHidden = hidden
				var namespace *queryNamespace
				var beforeColumns []queryColumn
				if existingNamespace {
					namespace = &queryNamespace{columns: []queryColumn{{descriptor: ColumnDescriptor{Name: "visible", Usage: integer.Usage()}}}}
					beforeColumns = append([]queryColumn(nil), namespace.columns...)
					b.queryNamespace = namespace
				}
				raw := parser.AggregateExpr{Name: "sum", Pos: parser.Position{Character: 10}, Args: []parser.Expr{one}}
				switch outcome {
				case "filter_failure":
					raw.Filter = one
				case "missing_target_column":
					raw.Args = []parser.Expr{parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "i"}}}, Pos: parser.Position{Character: 20}}}
				}
				node, err := b.bindSum(raw, name)
				if b.queryNamespace != namespace || len(b.queryHidden) != len(hidden) || &b.queryHidden[0] != &hidden[0] || !reflect.DeepEqual(b.queryHidden, beforeHidden) {
					t.Fatal("SUM leaked or replaced namespace/hidden candidates", b.queryNamespace, b.queryHidden)
				}
				if namespace != nil && !reflect.DeepEqual(namespace.columns, beforeColumns) {
					t.Fatal("SUM mutated the incumbent source namespace", namespace.columns)
				}
				if b.aggregateClause != "INSERT VALUES" || b.insertTarget != target || len(b.uses) != 0 {
					t.Fatal("SUM changed clause/target or read diagnostic-only destination", b.aggregateClause, b.insertTarget, b.uses)
				}
				if outcome == "success" {
					if err != nil || node == nil || node.ResultUsage().Type.ID() != sqltype.Int8 {
						t.Fatal("successful bindSum path", node, err)
					}
					return
				}
				want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Position: "20"}
				if outcome == "filter_failure" {
					want.Code, want.Message = "42804", "argument of FILTER must be type boolean, not type integer"
				} else {
					want.Code, want.Message = "42703", `column "i" does not exist`
					if !existingNamespace {
						want.Detail = `There is a column named "i" in table "sum_unit_target", but it cannot be referenced from this part of the query.`
					}
				}
				if node != nil || !reflect.DeepEqual(err, want) {
					t.Fatalf("analysis result = %v / %#v, want nil / %#v", node, err, want)
				}
			})
		}
	}
}

func TestSumAnalysisAssignmentBoundary(t *testing.T) {
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	column := ColumnDescriptor{Name: "i", Usage: integer.Usage()}
	one := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Pos: parser.Position{Character: 30}}}
	boolean := parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true, Pos: parser.Position{Character: 20}}}
	sum := parser.AggregateExpr{Name: "sum", Pos: parser.Position{Character: 10}, Args: []parser.Expr{boolean}, Filter: one}
	wrapped := parser.CastExpr{Child: parser.CastExpr{Child: sum, TypeName: parser.TypeName{Name: "integer"}}, TypeName: parser.TypeName{Name: "integer"}}
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.aggregateClause = "INSERT VALUES"
	policy := expressionPolicy{context: assignmentContext, expected: integer.Usage(), mode: sqltype.Assignment, column: column.Name}
	node, err := b.bind(wrapped, policy)
	wantAnalysis := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "argument of FILTER must be type boolean, not type integer", Position: "30"}
	if node != nil || !containsValuesAggregateAnalysis(wrapped) || !reflect.DeepEqual(err, wantAnalysis) {
		t.Fatalf("wrapped nil-source analysis = %v / %#v, want %#v", node, err, wantAnalysis)
	}
	node, err = b.bind(boolean, policy)
	if node == nil || err == nil || node.ResultUsage().Type.ID() != sqltype.Bool || containsValuesAggregateAnalysis(boolean) {
		t.Fatal("genuine assignment must retain its resolved source", node, err)
	}
	diagnostic := insertValueError(e.CatalogRead(), err, column, boolean, node.ResultUsage().Type)
	wantAssignment := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: `column "i" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression.", Position: "20"}
	if !reflect.DeepEqual(diagnostic, wantAssignment) {
		t.Fatalf("bound-source assignment = %#v, want %#v", diagnostic, wantAssignment)
	}
	if b.queryNamespace != nil || len(b.queryHidden) != 0 || b.aggregateClause != "INSERT VALUES" {
		t.Fatal("failed wrapped analysis changed subsequent assignment scope")
	}
}
