package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
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
			b.aggregateClause = "SELECT"
			raw := parser.FunctionExpr{
				Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}},
				Args: []parser.Expr{parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2}},
			}
			call, err := b.bindFunctionProvider(raw)
			if err != nil {
				t.Fatal(err)
			}
			if uint32(call.ResultUsage().Type.ID()) != tc.result || call.Properties().Volatility != expr.Immutable || isQuerySet(call) != tc.set {
				t.Fatal("typed immutable consumer contract", call)
			}
			captured, err := expressionRoutineDescriptor(call)
			if err != nil || captured == nil || captured.Builtin == nil {
				t.Fatal("missing actual bound declaration", captured, err)
			}
			identity := routineIdentity{tc.oid, ids.PGCatalogNamespaceOID, tc.name, "3802,4072,3802,16"}
			d := captured.Builtin
			if d.identity != identity || d.kind != functionRoutine || uint32(d.result) != tc.result || !d.strict || d.volatility != expr.Immutable || d.parallel != 's' || d.returnsSet != tc.set || d.defaults != 2 || d.variadic != 0 || d.providers != jsonRoutineProvider {
				t.Fatal("released full-IN declaration", d)
			}
			address := ids.ObjectAddress{ids.ProcedureClassID, tc.oid, 0}
			view := e.CatalogRead()
			if captured.address != address || captured.namespace != (ids.ObjectAddress{ids.NamespaceClassID, 11, 0}) || captured.name != identity.name || captured.Signature != identity.signature || uint32(captured.Result) != tc.result || captured.revision == 0 || view.Get(address) != captured {
				t.Fatal("declaration is not engine-owned", captured)
			}
			var args []expr.Expr
			boundExpressionChildren(call, func(arg expr.Expr) { args = append(args, arg) })
			if len(args) != 4 {
				t.Fatal("missing declared formals", args)
			}
			for i, id := range []sqltype.ID{sqltype.JSONB, sqltype.JSONPath, sqltype.JSONB, sqltype.Bool} {
				if args[i].ResultUsage().Type.ID() != id {
					t.Fatal("omission changed full signature", i, args[i])
				}
			}
			for slot, want := range map[int]string{2: "{}", 3: "f"} {
				value, err := args[slot].Eval(&EvalContext{Context: ctx})
				if err != nil || value.IsNull() {
					t.Fatal("default is not a real child", slot, value, err)
				}
				text, err := value.Type().Output(ctx, value)
				if err != nil || text != want {
					t.Fatal("released default", slot, text, err)
				}
			}
			e.mu.Lock()
			auth, err := e.authorizationLocked(b.op)
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err := admitRoutineDescriptor(auth, b.dependencies, captured, nil); err != nil {
				t.Fatal("original lifetime", err)
			}
			if err := admitRoutineDescriptor(auth, nil, captured, nil); err == nil {
				t.Fatal("repaired missing manifest")
			}
			checks := append([]expressionDependency(nil), b.dependencies...)
			for i := range checks {
				if checks[i].ref.Address == address {
					checks[i].revision++
				}
			}
			if err := admitRoutineDescriptor(auth, checks, captured, nil); err == nil {
				t.Fatal("admitted stale declaration")
			}
			if tc.set {
				b.defaultConsumer = true
				if _, err := b.bindFunctionProvider(raw); err == nil {
					t.Fatal("set admitted in scalar default")
				}
			} else if validFunctionSourceCall(call) {
				t.Fatal("scalar sibling admitted as source")
			}
		})
	}
}

func TestJSONBPathQueryInvocation(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	b.aggregateClause = "SELECT"
	literal := func(value string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: value}}
	}
	bind := func(doc, path string) expr.Expr {
		t.Helper()
		call, err := b.bindFunctionProvider(parser.FunctionExpr{
			Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "jsonb_path_query"}}},
			Args: []parser.Expr{literal(doc), literal(path)},
		})
		if err != nil {
			t.Fatal(err)
		}
		return call
	}
	call := bind(`[1,2]`, `$[*]`)
	c := &EvalContext{Context: ctx}
	cursor, err := openFunctionCall(c, call)
	if err != nil || cursor.kind != functionOwnedSetCursor || len(cursor.owned) != 2 {
		t.Fatal(cursor, err)
	}
	retained := cursor.owned[0]
	for _, want := range []string{"1", "2"} {
		v, present, err := cursor.next(ctx, call.ResultUsage().Type)
		text, outputErr := v.Type().Output(ctx, v)
		if err != nil || outputErr != nil || !present || text != want {
			t.Fatal(text, present, err, outputErr)
		}
	}
	if _, present, err := cursor.next(ctx, call.ResultUsage().Type); err != nil || present {
		t.Fatal("EOF", present, err)
	}
	if cursor.owned != nil {
		t.Fatal("EOF retained collection")
	}
	cursor.close()
	if cursor.owned != nil || !cursor.exhausted {
		t.Fatal("close retained collection", cursor)
	}
	text, err := retained.Type().Output(ctx, retained)
	if err != nil || text != "1" {
		t.Fatal("close invalidated returned value", text, err)
	}
	for range 2 {
		opened, err := openFunctionCall(c, call)
		if err != nil || opened.position != 0 || len(opened.owned) != 2 {
			t.Fatal("reopen inherited state", opened, err)
		}
		opened.close()
	}
	bad := bind(`[{"x":1},{}]`, `strict $[*].x`)
	if failed, err := openFunctionCall(c, bad); err == nil || failed.owned != nil {
		t.Fatal("late failure exposed prefix", failed, err)
	}
	invocation, err := openFunctionInvocation(c, &functionFromNode{calls: []expr.Expr{call, bad}})
	if err == nil || invocation.cursors != nil {
		t.Fatal("partial sibling open published invocation", invocation, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	opened, err := openFunctionCall(c, call)
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := opened.next(canceled, call.ResultUsage().Type); !errors.Is(err, context.Canceled) || present {
		t.Fatal("canceled cursor published row", present, err)
	}
	opened.close()
	if opened.owned != nil {
		t.Fatal("canceled close retained collection")
	}
	// Source argument replacement reconstructs typed usages and retains syntax,
	// so the same typed provider is used by target and source opening.
	rebuilt, err := withFunctionCallArguments(call, functionCallArguments(call))
	if err != nil || !sameQuerySetOccurrence(call, rebuilt) {
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
	reminted, err := openFunctionCall(c, mapped)
	if err != nil {
		t.Fatal("reminted source", err)
	}
	value, present, err := reminted.next(ctx, fresh[sqltype.JSONB])
	if err != nil || !present || value.Type() != fresh[sqltype.JSONB] {
		t.Fatal("reminted invocation authority", value, present, err)
	}
	reminted.close()
	// Each reopened source owns an ordinal counter distinct from its collection.
	source := srfFromUnitNode(t, call)
	for range 2 {
		stage, err := openQueryNode(ctx, srfFromUnitState(), source)
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
