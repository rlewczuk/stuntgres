package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func jsonArrayProviderUnit(t *testing.T, input, result sqltype.Usage, absent bool) functions.JSONArrayAggregateProvider {
	t.Helper()
	plan, err := sqltype.BindJSONArrayAggregate(input, result, absent)
	if err != nil {
		t.Fatal(err)
	}
	return functions.NewJSONArrayAggregateProvider(input, result, absent, plan)
}
func TestJSONArrayAggregateSlotsAndDescriptors(t *testing.T) {
	e := New()
	ctx := context.Background()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	b.Params.Parameters[1] = integer.Usage()
	for _, name := range []string{"json_agg", "jsonb_agg", "json_agg_strict", "jsonb_agg_strict", "json_agg"} {
		raw := parser.AggregateExpr{Name: name, Args: []parser.Expr{parser.ParameterExpr{Number: 1}}, Pos: parser.Position{Character: 8}}
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		id := sqltype.JSON
		if name == "jsonb_agg" || name == "jsonb_agg_strict" {
			id = sqltype.JSONB
		}
		owner, _ := e.CatalogRead().LookupType(id)
		expectedOID := sqltype.ID(114)
		if name == "jsonb_agg" || name == "jsonb_agg_strict" {
			expectedOID = 3802
		}
		if node.ResultUsage().Type.ID() != expectedOID {
			t.Fatal("fixed JSON family identity changed", node.ResultUsage())
		}
		if node.ResultUsage().Type != owner || node.ResultUsage().Typmod != -1 || node.ResultUsage().Collation != (sqltype.CollationRef{}) || !node.Properties().StageDependent {
			t.Fatal("result descriptor", node.ResultUsage())
		}
	}
	if len(b.Grouping.Aggregates) != 4 {
		t.Fatal("incompatible operations shared a slot", len(b.Grouping.Aggregates))
	}
	for _, call := range b.Grouping.Aggregates {
		if !call.IncludeNulls {
			t.Fatal("presence lost before provider")
		}
	}
	for _, absent := range []bool{true, false, true} {
		raw := parser.AggregateExpr{Name: "json_arrayagg", Args: []parser.Expr{parser.ParameterExpr{Number: 1}}, JSON: &parser.JSONAggregateOptions{Absent: absent}}
		if _, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.Grouping.Aggregates) != 6 {
		t.Fatal("NULL policy identity", len(b.Grouping.Aggregates))
	}
	schemaBinder := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	schemaBinder.Params.Parameters[1] = integer.Usage()
	targets := []parser.Expr{}
	for _, name := range []string{"json_agg", "jsonb_agg", "json_agg_strict", "jsonb_agg_strict", "json_arrayagg"} {
		call := parser.AggregateExpr{Name: name, Args: []parser.Expr{parser.ParameterExpr{Number: 1}}}
		if name == "json_arrayagg" {
			call.JSON = &parser.JSONAggregateOptions{Absent: true}
		}
		targets = append(targets, call)
	}
	query, err := schemaBinder.BindSelect(&parser.Select{Targets: targets}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range query.Root.Schema() {
		if column.Nullable || column.NullabilityKnown || column.OriginTable != 0 || column.OriginAttribute != 0 || column.Usage.Typmod != -1 || column.Usage.Collation != (sqltype.CollationRef{}) {
			t.Fatal("computed JSON aggregate descriptor became a source-column claim", column)
		}
	}
}

func TestJSONArrayAggregateProviderRemap(t *testing.T) {
	source, destination := New(), New()
	ctx := context.Background()
	input, _ := source.CatalogRead().LookupType(sqltype.Int4)
	for _, id := range []sqltype.ID{sqltype.JSON, sqltype.JSONB} {
		result, _ := source.CatalogRead().LookupType(id)
		p := jsonArrayProviderUnit(t, input.Usage(), result.Usage(), true)
		root := &plan.AggregateNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: result.Usage(), Nullable: true, NullabilityKnown: true}}, Child: &plan.ValuesNode{}, Calls: []plan.AggregateCall{{Provider: p, Operation: functions.AggregateJSONStrict, IncludeNulls: true}}}
		mapped, err := RemapViewQuery(source.CatalogRead(), root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
			next, _ := destination.CatalogRead().LookupType(old.ID())
			return next
		})
		if err != nil {
			t.Fatal(err)
		}
		call := mapped.(*plan.AggregateNode).Calls[0]
		newInput, _ := destination.CatalogRead().LookupType(sqltype.Int4)
		newResult, _ := destination.CatalogRead().LookupType(id)
		if call.Provider.ResultUsage().Type != newResult || mapped.Schema()[0].Usage.Type != newResult || p.ResultUsage().Type != result || !call.IncludeNulls {
			t.Fatal("incomplete immutable remap")
		}
		state, err := call.Provider.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		old, _ := sqltype.Integer(input, 1)
		next, _ := sqltype.Integer(newInput, 1)
		if err = call.Provider.Advance(ctx, state, old, sqltype.Value{}); err == nil {
			t.Fatal("old owner accepted")
		}
		if err = call.Provider.Advance(ctx, state, next, sqltype.Value{}); err != nil {
			t.Fatal("remapped plan not rebound", err)
		}
		value, err := call.Provider.Finalize(ctx, state)
		if err != nil {
			t.Fatal(err)
		}
		text, outputErr := value.Type().Output(ctx, value)
		if outputErr != nil || value.Type() != newResult || text != "[1]" {
			t.Fatal(text, outputErr)
		}
	}
}

func TestJSONArrayAggregateRunnerNullPresence(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		for _, rejected := range []bool{false, true} {
			qtext := "SELECT json_agg_strict(v"
			if ordered {
				qtext += " ORDER BY v"
			}
			qtext += ")"
			if rejected {
				qtext += " FILTER(WHERE FALSE)"
			}
			qtext += " FROM t"
			_, q, source := queryCoreFixture(t, qtext)
			node := q.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
			usage := node.Calls[0].Argument.ResultUsage()
			node.Calls[0].Argument = queryProbe(usage, func(*exec.Context) (sqltype.Value, error) {
				if rejected {
					t.Fatal("FILTER evaluated rejected argument")
				}
				return sqltype.Null(usage.Type), nil
			})
			rows, n, err := exec.Finalize(context.Background(), q.Root, source, nil)
			if err != nil || n != 1 {
				t.Fatal(n, err)
			}
			values := make([]sqltype.Value, 1)
			present, err := rows.Next(context.Background(), values)
			if !present || err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if rejected {
				if !values[0].IsNull() {
					rows.Close()
					t.Fatal("rejected input marked present")
				}
			} else {
				text, outputErr := values[0].Type().Output(context.Background(), values[0])
				if outputErr != nil || values[0].IsNull() || text != "[]" {
					rows.Close()
					t.Fatal("seen NULL lost", text, outputErr)
				}
			}
			rows.Close()
		}
	}
}

func TestJSONArrayAggregateReachedEagerCancellation(t *testing.T) {
	for _, tc := range []struct{ name, target string }{
		{"json_agg", ".writeJSONBString"}, {"jsonb_agg", ".jsonbOutput"}, {"jsonb_agg", ".(*jsonbBuilder).close"},
	} {
		e, q, source := queryCoreFixture(t, "SELECT "+tc.name+"(v) FROM t")
		node := q.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
		input, _ := e.CatalogRead().LookupType(sqltype.Text)
		result := node.Calls[0].Provider.ResultUsage()
		node.Calls[0].Provider = jsonArrayProviderUnit(t, input.Usage(), result, false)
		node.Calls[0].Argument = queryProbe(input.Usage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.String(input, "é\"\\"), nil })
		ctx, cancel := context.WithCancel(context.Background())
		probe := &bitwiseAggregateCancelContext{Context: ctx, cancel: cancel, target: tc.target}
		rows, n, err := exec.Finalize(probe, q.Root, source, nil)
		cancel()
		if !probe.entered || rows != nil || n != 0 || !errors.Is(err, context.Canceled) {
			if rows != nil {
				rows.Close()
			}
			t.Fatal("actual conversion/tree/output cancellation published result", tc, probe.entered, n, err)
		}
		if source.View != nil || source.Anonymous != nil {
			t.Fatal("canceled source retained")
		}
	}
}

func TestJSONArrayAggregateInsertReachedCancellation(t *testing.T) {
	for _, target := range []string{".writeJSONBString", ".jsonbOutput", ".(*jsonbBuilder).close"} {
		e := New()
		run(t, e, "CREATE TABLE ja_cancel_source(v text)")
		run(t, e, "INSERT INTO ja_cancel_source VALUES('one'),('two')")
		run(t, e, "CREATE TABLE ja_cancel_destination(v jsonb)")
		run(t, e, "INSERT INTO ja_cancel_destination VALUES('[9]')")
		ctx, cancel := context.WithCancel(context.Background())
		probe := &bitwiseAggregateCancelContext{Context: ctx, cancel: cancel, target: target}
		op, done, err := testOperation(e, probe)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		// Acquire owns a cancelCtx; instrument only the admitted operation so the
		// actual execution retains this reached-frame context's Err observation.
		op.Ctx = probe
		scope, err := e.StartStatement(op)
		if err != nil {
			done()
			cancel()
			t.Fatal(err)
		}
		name := "jsonb_agg"
		if target == ".writeJSONBString" {
			name = "json_agg"
		}
		result, err := scope.Execute(statement(t, "INSERT INTO ja_cancel_destination SELECT "+name+"(v) FROM ja_cancel_source"), 0, nil)
		finish := scope.Finish(err)
		done()
		cancel()
		if !probe.entered || !errors.Is(err, context.Canceled) || !errors.Is(finish, context.Canceled) || result.Rows != nil || result.Count != 0 {
			if result.Rows != nil {
				result.Rows.Close()
			}
			t.Fatal("actual aggregate cancellation published INSERT", target, probe.entered, result.Count, err, finish)
		}
		current := run(t, e, "SELECT v FROM ja_cancel_destination")
		values := make([]sqltype.Value, 1)
		present, rowErr := current.Rows.Next(context.Background(), values)
		if rowErr != nil || !present {
			current.Rows.Close()
			t.Fatal("destination sentinel missing", rowErr)
		}
		text, outputErr := values[0].Type().Output(context.Background(), values[0])
		extra, endErr := current.Rows.Next(context.Background(), values)
		current.Rows.Close()
		if outputErr != nil || text != "[9]" || extra || endErr != nil {
			t.Fatal("canceled aggregate changed destination", text, outputErr, extra, endErr)
		}
	}
}

func TestJSONArrayAggregateStoredReturningPresentation(t *testing.T) {
	e := New()
	ctx := context.Background()
	raw := statement(t, "SELECT json_arrayagg('[1]'::text FORMAT JSON NULL ON NULL RETURNING varchar(10) FORMAT JSON)").(*parser.Select).Targets[0]
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if err = b.CaptureViewLiterals(raw, bound, analyze.ViewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	renderer := NewQueryRenderer(NewView(ViewParts{Literals: b.Capture.Literals, CastUsages: b.Capture.CastUsages}))
	renderer.Frame = b.FrameID()
	text, err := renderer.ExpressionSQL(ctx, e.CatalogRead(), raw, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.Parse("SELECT " + text)
	if err != nil || len(parsed) != 1 {
		t.Fatal("stored expression cannot replay", text, err)
	}
	call, ok := parsed[0].(*parser.Select).Targets[0].(parser.AggregateExpr)
	if !ok || call.JSON == nil || call.JSON.Input == nil || call.JSON.Output == nil || call.JSON.Absent || call.JSON.Returning == nil || len(call.JSON.Returning.Modifiers) != 1 || *call.JSON.Returning.Modifiers[0].Integer != 10 {
		t.Fatal("stored SQL/JSON semantics lost", call)
	}
	_, query, source := queryCoreFixture(t, "SELECT "+text+" FROM t WHERE id=1")
	rows, n, err := exec.Finalize(ctx, query.Root, source, nil)
	if err != nil || n != 1 {
		t.Fatal("stored expression replay", n, err)
	}
	defer rows.Close()
	values := make([]sqltype.Value, 1)
	present, err := rows.Next(ctx, values)
	got, _ := values[0].Text()
	if !present || err != nil || got != "[[1]]" || values[0].Type().ID() != sqltype.VarChar {
		t.Fatal("stored RETURNING/input FORMAT changed", got, err)
	}
}
