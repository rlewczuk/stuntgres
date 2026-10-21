package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func jsonObjectProviderUnit(t *testing.T, key, input, result sqltype.Usage, strict bool) functions.JSONObjectAggregate {
	t.Helper()
	p, err := functions.NewJSONObjectAggregate(result, strict, false).Complete(key, input)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestJSONObjectAggregateProviderEnvironment(t *testing.T) {
	e := New()
	ctx := context.Background()
	bytes, _ := e.CatalogRead().LookupType(sqltype.Bytea)
	result, _ := e.CatalogRead().LookupType(sqltype.JSON)
	p := jsonObjectProviderUnit(t, bytes.Usage(), bytes.Usage(), result.Usage(), false)
	value, err := sqltype.Binary(bytes, "AB")
	if err != nil {
		t.Fatal(err)
	}
	hex, err := p.InitWithEnvironment(ctx, sqltype.OutputEnvironment{ByteaOutput: sqltype.ByteaHex})
	if err != nil {
		t.Fatal(err)
	}
	escape, err := p.InitWithEnvironment(ctx, sqltype.OutputEnvironment{ByteaOutput: sqltype.ByteaEscape})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []any{hex, escape} {
		if err = p.Advance(ctx, state, value, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		state any
		want  string
	}{{hex, `{ "\\x4142" : "\\x4142" }`}, {escape, `{ "AB" : "AB" }`}} {
		v, err := p.Finalize(ctx, tc.state)
		if err != nil {
			t.Fatal(err)
		}
		text, err := v.Type().Output(ctx, v)
		if err != nil || text != tc.want || v.Type() != result {
			t.Fatal("shared output environment", text, err)
		}
	}
	if err = p.Advance(ctx, struct{}{}, value, value); err == nil {
		t.Fatal("foreign state accepted")
	}
	if _, err = p.Finalize(ctx, nil); err == nil {
		t.Fatal("nil state accepted")
	}
}

func TestJSONObjectAggregateSlotsAndDescriptors(t *testing.T) {
	e := New()
	ctx := context.Background()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	b.Params.Parameters[1], b.Params.Parameters[2] = text.Usage(), integer.Usage()
	for _, name := range []string{"json_object_agg", "jsonb_object_agg", "json_object_agg_strict", "jsonb_object_agg_strict", "json_object_agg"} {
		raw := parser.AggregateExpr{Name: name, Args: []parser.Expr{parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2}}, Pos: parser.Position{Character: 8}}
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		id := sqltype.JSON
		if name == "jsonb_object_agg" || name == "jsonb_object_agg_strict" {
			id = sqltype.JSONB
		}
		owner, _ := e.CatalogRead().LookupType(id)
		if node.ResultUsage().Type != owner || node.ResultUsage().Typmod != -1 || node.ResultUsage().Collation != (sqltype.CollationRef{}) || !node.Properties().StageDependent {
			t.Fatal("fixed computed descriptor", node.ResultUsage())
		}
	}
	if len(b.Grouping.Aggregates) != 4 {
		t.Fatal("incompatible object calls share slots", len(b.Grouping.Aggregates))
	}
	for _, call := range b.Grouping.Aggregates {
		if !call.IncludeNulls || call.Argument.ResultUsage().Type != text || call.Separator.ResultUsage().Type != integer {
			t.Fatal("two inputs/presence lost", call)
		}
	}
	if _, err := b.Bind(parser.AggregateExpr{Name: "json_agg", Args: []parser.Expr{parser.ParameterExpr{Number: 2}}}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err != nil {
		t.Fatal(err)
	}
	if len(b.Grouping.Aggregates) != 5 {
		t.Fatal("array/object slot collision")
	}
	schemaBinder := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	schemaBinder.Params.Parameters[1], schemaBinder.Params.Parameters[2] = text.Usage(), integer.Usage()
	targets := []parser.Expr{}
	for _, name := range []string{"json_object_agg", "jsonb_object_agg", "json_object_agg_strict", "jsonb_object_agg_strict"} {
		targets = append(targets, parser.AggregateExpr{Name: name, Args: []parser.Expr{parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2}}})
	}
	query, err := schemaBinder.BindSelect(&parser.Select{Targets: targets}, false)
	if err != nil {
		t.Fatal(err)
	}
	for i, column := range query.Root.Schema() {
		id := sqltype.ID(114)
		if i == 1 || i == 3 {
			id = 3802
		}
		if column.Usage.Type.ID() != id || column.Nullable || column.NullabilityKnown || column.OriginTable != 0 || column.OriginAttribute != 0 || column.Usage.Typmod != -1 || column.Usage.Collation != (sqltype.CollationRef{}) {
			t.Fatal("computed result gained source authority", column)
		}
	}
}

func TestJSONObjectAggregateProviderRemap(t *testing.T) {
	source, destination := New(), New()
	ctx := context.Background()
	key, _ := source.CatalogRead().LookupType(sqltype.Text)
	input, _ := source.CatalogRead().LookupType(sqltype.Int4)
	for _, id := range []sqltype.ID{sqltype.JSON, sqltype.JSONB} {
		result, _ := source.CatalogRead().LookupType(id)
		p := jsonObjectProviderUnit(t, key.Usage(), input.Usage(), result.Usage(), true)
		root := &plan.AggregateNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: result.Usage(), Nullable: true, NullabilityKnown: true}}, Child: &plan.ValuesNode{}, Calls: []plan.AggregateCall{{Provider: p, Operation: functions.AggregateJSONObjectStrict, IncludeNulls: true}}}
		mapped, err := stored.RemapViewQuery(source.CatalogRead(), root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
			next, _ := destination.CatalogRead().LookupType(old.ID())
			return next
		})
		if err != nil {
			t.Fatal(err)
		}
		call := mapped.(*plan.AggregateNode).Calls[0]
		newKey, _ := destination.CatalogRead().LookupType(sqltype.Text)
		newInput, _ := destination.CatalogRead().LookupType(sqltype.Int4)
		newResult, _ := destination.CatalogRead().LookupType(id)
		provider := call.Provider.(functions.JSONObjectAggregate)
		if provider.KeyUsage().Type != newKey || provider.InputUsage().Type != newInput || provider.ResultUsage().Type != newResult || mapped.Schema()[0].Usage.Type != newResult || p.KeyUsage().Type != key || p.InputUsage().Type != input || !call.IncludeNulls {
			t.Fatal("incomplete immutable remap")
		}
		state, err := provider.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		old, _ := sqltype.Integer(input, 1)
		next, _ := sqltype.Integer(newInput, 1)
		if err = provider.Advance(ctx, state, sqltype.String(key, "a"), next); err == nil {
			t.Fatal("old key accepted")
		}
		if err = provider.Advance(ctx, state, sqltype.String(newKey, "a"), old); err == nil {
			t.Fatal("old value accepted")
		}
		if err = provider.Advance(ctx, state, sqltype.String(newKey, "a"), next); err != nil {
			t.Fatal("remapped plan not rebound", err)
		}
		v, err := provider.Finalize(ctx, state)
		if err != nil {
			t.Fatal(err)
		}
		got, err := v.Type().Output(ctx, v)
		want := `{ "a" : 1 }`
		if id == sqltype.JSONB {
			want = `{"a": 1}`
		}
		if err != nil || got != want || v.Type() != newResult {
			t.Fatal("remapped output", got, err)
		}
	}
}

func TestJSONObjectAggregateRunnerNullPresence(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		for _, rejected := range []bool{false, true} {
			qtext := "SELECT json_object_agg_strict(id,v"
			if ordered {
				qtext += " ORDER BY id"
			}
			qtext += ")"
			if rejected {
				qtext += " FILTER(WHERE FALSE)"
			}
			qtext += " FROM t"
			_, q, source := queryCoreFixture(t, qtext)
			node := q.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
			keyUsage, valueUsage := node.Calls[0].Argument.ResultUsage(), node.Calls[0].Separator.ResultUsage()
			keys, values := 0, 0
			node.Calls[0].Argument = queryProbe(keyUsage, func(*exec.Context) (sqltype.Value, error) {
				if rejected {
					t.Fatal("FILTER evaluated key")
				}
				keys++
				return sqltype.Integer(keyUsage.Type, 1)
			})
			node.Calls[0].Separator = queryProbe(valueUsage, func(*exec.Context) (sqltype.Value, error) {
				if rejected {
					t.Fatal("FILTER evaluated value")
				}
				values++
				return sqltype.Null(valueUsage.Type), nil
			})
			rows, n, err := exec.Finalize(context.Background(), q.Root, source, nil)
			if err != nil || n != 1 {
				t.Fatal(n, err)
			}
			out := make([]sqltype.Value, 1)
			present, err := rows.Next(context.Background(), out)
			if !present || err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if rejected {
				if !out[0].IsNull() || keys != 0 || values != 0 {
					rows.Close()
					t.Fatal("filtered input admitted")
				}
			} else {
				text, err := out[0].Type().Output(context.Background(), out[0])
				if err != nil || out[0].IsNull() || text != "{  }" || keys != 4 || values != 4 {
					rows.Close()
					t.Fatal("two-slot NULL presence", text, keys, values, err)
				}
			}
			rows.Close()
		}
	}
}

func TestJSONObjectAggregateReachedEagerCancellation(t *testing.T) {
	for _, tc := range []struct{ name, target string }{{"json_object_agg", ".writeJSONBString"}, {"jsonb_object_agg_strict", ".sortJSONBFields"}, {"jsonb_object_agg", ".jsonbOutput"}} {
		e, q, source := queryCoreFixture(t, "SELECT "+tc.name+"(id,v) FROM t")
		node := q.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
		input, _ := e.CatalogRead().LookupType(sqltype.Text)
		result := node.Calls[0].Provider.ResultUsage()
		node.Calls[0].Provider = jsonObjectProviderUnit(t, input.Usage(), input.Usage(), result, tc.name == "jsonb_object_agg_strict")
		node.Calls[0].Argument = queryProbe(input.Usage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.String(input, "é\"\\"), nil })
		node.Calls[0].Separator = queryProbe(input.Usage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.String(input, "value"), nil })
		ctx, cancel := context.WithCancel(context.Background())
		probe := &bitwiseAggregateCancelContext{Context: ctx, cancel: cancel, target: tc.target}
		rows, n, err := exec.Finalize(probe, q.Root, source, nil)
		cancel()
		if !probe.entered || rows != nil || n != 0 || !errors.Is(err, context.Canceled) {
			if rows != nil {
				rows.Close()
			}
			t.Fatal("cancellation published result", tc, probe.entered, n, err)
		}
		if source.View != nil || source.Anonymous != nil {
			t.Fatal("canceled source retained")
		}
	}
}

func TestJSONObjectAggregateInsertReachedCancellation(t *testing.T) {
	for _, target := range []string{".writeJSONBString", ".compactJSONBFields", ".jsonbOutput"} {
		e := New()
		run(t, e, "CREATE TABLE jo_cancel_source(k text,v text)")
		run(t, e, "INSERT INTO jo_cancel_source VALUES('a','one'),('b','two')")
		run(t, e, "CREATE TABLE jo_cancel_destination(v jsonb)")
		run(t, e, `INSERT INTO jo_cancel_destination VALUES('{"sentinel":9}')`)
		ctx, cancel := context.WithCancel(context.Background())
		probe := &bitwiseAggregateCancelContext{Context: ctx, cancel: cancel, target: target}
		op, done, err := testOperation(e, probe)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		op.Ctx = probe
		scope, err := e.StartStatement(op)
		if err != nil {
			done()
			cancel()
			t.Fatal(err)
		}
		name := "jsonb_object_agg_strict"
		if target == ".writeJSONBString" {
			name = "json_object_agg"
		}
		result, err := scope.Execute(statement(t, "INSERT INTO jo_cancel_destination SELECT "+name+"(k,v) FROM jo_cancel_source"), 0, nil)
		finish := scope.Finish(err)
		done()
		cancel()
		if !probe.entered || !errors.Is(err, context.Canceled) || !errors.Is(finish, context.Canceled) || result.Rows != nil || result.Count != 0 {
			if result.Rows != nil {
				result.Rows.Close()
			}
			t.Fatal("canceled INSERT published", target, probe.entered, result.Count, err, finish)
		}
		current := run(t, e, "SELECT v FROM jo_cancel_destination")
		values := make([]sqltype.Value, 1)
		present, rowErr := current.Rows.Next(context.Background(), values)
		if rowErr != nil || !present {
			current.Rows.Close()
			t.Fatal("sentinel missing", rowErr)
		}
		text, outputErr := values[0].Type().Output(context.Background(), values[0])
		extra, endErr := current.Rows.Next(context.Background(), values)
		current.Rows.Close()
		if outputErr != nil || text != `{"sentinel": 9}` || extra || endErr != nil {
			t.Fatal("canceled INSERT changed sentinel", text, outputErr, extra, endErr)
		}
	}
}
