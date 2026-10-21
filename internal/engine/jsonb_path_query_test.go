package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestJSONBPathQueryBinding(t *testing.T) {
	for _, tc := range []struct {
		name   string
		oid    ids.OID
		result uint32
		set    bool
	}{
		{"jsonb_path_exists", 4005, 16, false},
		{"jsonb_path_match", 4009, 16, false},
		{"jsonb_path_query_first", 4008, 3802, false},
		{"jsonb_path_query", 4006, 3802, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			e := New()
			b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			b.Clause.AggregateClause = "SELECT"
			raw := parser.FunctionExpr{
				Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}},
				Args: []parser.Expr{parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2}},
			}
			call, err := b.BindFunctionProvider(raw)
			if err != nil {
				t.Fatal(err)
			}
			if uint32(call.ResultUsage().Type.ID()) != tc.result || call.Properties().Volatility != expr.Immutable || exec.SourceReturnsSet(call) != tc.set {
				t.Fatal("typed immutable consumer contract", call)
			}
			captured, err := exec.ExpressionRoutineDescriptor(call)
			if err != nil || captured == nil || captured.Builtin == nil {
				t.Fatal("missing actual bound declaration", captured, err)
			}
			identity := catalog.RoutineIdentity{tc.oid, ids.PGCatalogNamespaceOID, tc.name, "3802,4072,3802,16"}
			d := captured.Builtin
			if d.Identity != identity || d.Kind != catalog.FunctionRoutine || uint32(d.Result) != tc.result || !d.Strict || d.Volatility != expr.Immutable || d.Parallel != 's' || d.ReturnsSet != tc.set || d.Defaults != 2 || d.Variadic != 0 || d.Providers != functions.JSONRoutineProvider {
				t.Fatal("released full-IN declaration", d)
			}
			address := ids.ObjectAddress{ids.ProcedureClassID, tc.oid, 0}
			view := e.CatalogRead()
			if captured.Address() != address || captured.Namespace() != (ids.ObjectAddress{ids.NamespaceClassID, 11, 0}) || captured.Name() != identity.Name || captured.Signature != identity.Signature || uint32(captured.Result) != tc.result || captured.Revision() == 0 || view.Get(address) != captured {
				t.Fatal("declaration is not engine-owned", captured)
			}
			var args []expr.Expr
			plan.ExpressionChildren(call, func(arg expr.Expr) { args = append(args, arg) })
			if len(args) != 4 {
				t.Fatal("missing declared formals", args)
			}
			for i, id := range []sqltype.ID{sqltype.JSONB, sqltype.JSONPath, sqltype.JSONB, sqltype.Bool} {
				if args[i].ResultUsage().Type.ID() != id {
					t.Fatal("omission changed full signature", i, args[i])
				}
			}
			for slot, want := range map[int]string{2: "{}", 3: "f"} {
				value, err := args[slot].Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
				if err != nil || value.IsNull() {
					t.Fatal("default is not a real child", slot, value, err)
				}
				text, err := value.Type().Output(ctx, value)
				if err != nil || text != want {
					t.Fatal("released default", slot, text, err)
				}
			}
			e.mu.Lock()
			auth, err := e.authorizationLocked(binderOperation(b))
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err := admitRoutineDescriptor(routineAccessOf(auth), b.Dependencies, captured, nil); err != nil {
				t.Fatal("original lifetime", err)
			}
			if err := admitRoutineDescriptor(routineAccessOf(auth), nil, captured, nil); err == nil {
				t.Fatal("repaired missing manifest")
			}
			checks := append([]catalog.ExpressionDependency(nil), b.Dependencies...)
			for i := range checks {
				if checks[i].Ref.Address == address {
					checks[i].Revision++
				}
			}
			if err := admitRoutineDescriptor(routineAccessOf(auth), checks, captured, nil); err == nil {
				t.Fatal("admitted stale declaration")
			}
			if tc.set {
				b.Consumer.DefaultConsumer = true
				if _, err := b.BindFunctionProvider(raw); err == nil {
					t.Fatal("set admitted in scalar default")
				}
			} else if exec.SourceAdmitted(call) {
				t.Fatal("scalar sibling admitted as source")
			}
		})
	}
}

func TestJSONBPathQueryInvocation(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	literal := func(value string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: value}}
	}
	bind := func(doc, path string) expr.Expr {
		t.Helper()
		call, err := b.BindFunctionProvider(parser.FunctionExpr{
			Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "jsonb_path_query"}}},
			Args: []parser.Expr{literal(doc), literal(path)},
		})
		if err != nil {
			t.Fatal(err)
		}
		return call
	}
	call := bind(`[1,2]`, `$[*]`)
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
	cursor, err := exec.OpenFunctionSource(c, call)
	if err != nil || len(cursor.Values) != 2 {
		t.Fatal(cursor, err)
	}
	retained := cursor.Values[0]
	for _, want := range []string{"1", "2"} {
		v, present, err := cursor.Next(ctx)
		text, outputErr := v.Type().Output(ctx, v)
		if err != nil || outputErr != nil || !present || text != want {
			t.Fatal(text, present, err, outputErr)
		}
	}
	if _, present, err := cursor.Next(ctx); err != nil || present {
		t.Fatal("EOF", present, err)
	}
	if cursor.Values != nil {
		t.Fatal("EOF retained collection")
	}
	cursor.Close()
	if cursor.Values != nil || !cursor.Exhausted {
		t.Fatal("close retained collection", cursor)
	}
	text, err := retained.Type().Output(ctx, retained)
	if err != nil || text != "1" {
		t.Fatal("close invalidated returned value", text, err)
	}
	for range 2 {
		opened, err := exec.OpenFunctionSource(c, call)
		if err != nil || opened.Position != 0 || len(opened.Values) != 2 {
			t.Fatal("reopen inherited state", opened, err)
		}
		opened.Close()
	}
	bad := bind(`[{"x":1},{}]`, `strict $[*].x`)
	if failed, err := exec.OpenFunctionSource(c, bad); err == nil || failed.Values != nil {
		t.Fatal("late failure exposed prefix", failed, err)
	}
	invocation, err := exec.OpenFunctionInvocation(c, &plan.FunctionFromNode{Calls: []expr.Expr{call, bad}})
	if err == nil || invocation.Cursors != nil {
		t.Fatal("partial sibling open published invocation", invocation, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	opened, err := exec.OpenFunctionSource(c, call)
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := opened.Next(canceled); !errors.Is(err, context.Canceled) || present {
		t.Fatal("canceled cursor published row", present, err)
	}
	opened.Close()
	if opened.Values != nil {
		t.Fatal("canceled close retained collection")
	}
	// Source argument replacement reconstructs typed usages and retains syntax,
	// so the same typed provider is used by target and source opening.
	rebuilt, err := expr.Substitute(expr.Expr(call).(expr.Composite), expr.InfoOf(call), expr.Expr(call).(expr.Composite).Children())
	if err != nil || !analyze.SameQuerySetOccurrence(call, rebuilt) {
		t.Fatal("source reconstruction", rebuilt, err)
	}
	if _, ok := rebuilt.(functions.JSONBPathQuery); !ok {
		t.Fatal("source replaced typed provider", rebuilt)
	}
	fresh := jsonObjectUnitTypes()
	mapped, changed, err := retagBoundTypes(e.CatalogRead(), call, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil || !changed {
		t.Fatal("remint", changed, err)
	}
	reminted, err := exec.OpenFunctionSource(c, mapped)
	if err != nil {
		t.Fatal("reminted source", err)
	}
	value, present, err := reminted.Next(ctx)
	if err != nil || !present || value.Type() != fresh[sqltype.JSONB] {
		t.Fatal("reminted invocation authority", value, present, err)
	}
	reminted.Close()
	// Each reopened source owns an ordinal counter distinct from its collection.
	source := srfFromUnitNode(t, call)
	for range 2 {
		stage, err := exec.OpenNode(ctx, srfFromUnitState(), source)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stage.Close()
			values := make([]sqltype.Value, 2)
			for _, want := range []int64{1, 2} {
				present, err := stage.Next(ctx, values)
				if err != nil || !present {
					t.Fatal("source row", present, err)
				}
				ordinal, err := values[1].Integer()
				if err != nil || ordinal != want {
					t.Fatal("ordinal did not restart", ordinal, err)
				}
			}
			if present, err := stage.Next(ctx, values); err != nil || present {
				t.Fatal("source EOF", present, err)
			}
		}()
	}
}
