package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Public pg_proc inquiry has a bounded native column surface. Inspect the real
// bound provider and its owned lifetime here; public ACL behavior stays in reference.
func TestJSONBPathQueryArrayDeclaration(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "jsonb_path_query_array"}}},
		Args: []parser.Expr{parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2}},
	}
	node, err := b.bindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	call := node.(functions.JSONBPathQueryArray)
	if call.ResultUsage().Type.ID() != sqltype.JSONB || call.Properties().Volatility != Immutable || isQuerySet(call) {
		t.Fatal("scalar immutable JSONB contract", call)
	}
	captured, err := expressionRoutineDescriptor(call)
	if err != nil || captured == nil || captured.builtin == nil {
		t.Fatal("missing actual bound declaration", captured, err)
	}
	address := ids.ObjectAddress{ids.ProcedureClassID, 4007, 0}
	identity := routineIdentity{4007, ids.PGCatalogNamespaceOID, "jsonb_path_query_array", "3802,4072,3802,16"}
	d := captured.builtin
	if d.identity != identity || d.kind != functionRoutine || d.result != 3802 || !d.strict || d.volatility != Immutable || d.parallel != 's' || d.returnsSet || d.defaults != 2 || d.variadic != 0 || d.providers != jsonRoutineProvider {
		t.Fatal("released full-IN declaration", d)
	}
	view := e.CatalogRead()
	if captured.address != address || captured.namespace != (ids.ObjectAddress{ids.NamespaceClassID, 11, 0}) || captured.name != identity.name || captured.signature != identity.signature || captured.result != 3802 || captured.routineKind != functionRoutine || !captured.strict || captured.volatility != Immutable || captured.revision == 0 || view.state.descriptors[address] != captured {
		t.Fatal("bound descriptor is not the owned released routine", captured)
	}
	for i, id := range []sqltype.ID{3802, 4072, 3802, 16} {
		if call.Args[i].ResultUsage().Type.ID() != id {
			t.Fatal("omission changed full declared input contract", i, call.Args[i].ResultUsage())
		}
	}
	for slot, want := range map[int]string{2: "{}", 3: "f"} {
		value, err := call.Args[slot].Eval(&EvalContext{Context: ctx})
		if err != nil || value.IsNull() {
			t.Fatal("default is not a real nonnull child", slot, value, err)
		}
		text, err := value.Type().Output(ctx, value)
		if err != nil || text != want {
			t.Fatal("released default value", slot, text, err)
		}
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(b.op)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := admitRoutineDescriptor(auth, b.dependencies, captured, nil); err != nil {
		t.Fatal("original omitted-argument binding authority", err)
	}
	if err := admitRoutineDescriptor(auth, nil, captured, nil); err == nil {
		t.Fatal("current OID repaired missing original manifest")
	}
	checks := append([]expressionDependency(nil), b.dependencies...)
	for i := range checks {
		if checks[i].ref.Address == address {
			checks[i].revision++
		}
	}
	if err := admitRoutineDescriptor(auth, checks, captured, nil); err == nil {
		t.Fatal("stale full-signature routine lifetime admitted")
	}
}
