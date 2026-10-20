package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
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
	if call.ResultUsage().Type.ID() != sqltype.JSONB || call.Properties().Volatility != expr.Immutable || exec.SourceReturnsSet(call) {
		t.Fatal("scalar immutable JSONB contract", call)
	}
	captured, err := routine.ExpressionRoutineDescriptor(call)
	if err != nil || captured == nil || captured.Builtin == nil {
		t.Fatal("missing actual bound declaration", captured, err)
	}
	address := ids.ObjectAddress{ids.ProcedureClassID, 4007, 0}
	identity := catalog.RoutineIdentity{4007, ids.PGCatalogNamespaceOID, "jsonb_path_query_array", "3802,4072,3802,16"}
	d := captured.Builtin
	if d.Identity != identity || d.Kind != catalog.FunctionRoutine || d.Result != 3802 || !d.Strict || d.Volatility != expr.Immutable || d.Parallel != 's' || d.ReturnsSet || d.Defaults != 2 || d.Variadic != 0 || d.Providers != functions.JSONRoutineProvider {
		t.Fatal("released full-IN declaration", d)
	}
	view := e.CatalogRead()
	if captured.Address() != address || captured.Namespace() != (ids.ObjectAddress{ids.NamespaceClassID, 11, 0}) || captured.Name() != identity.Name || captured.Signature != identity.Signature || captured.Result != 3802 || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Immutable || captured.Revision() == 0 || view.Get(address) != captured {
		t.Fatal("bound descriptor is not the owned released routine", captured)
	}
	for i, id := range []sqltype.ID{3802, 4072, 3802, 16} {
		if call.Args[i].ResultUsage().Type.ID() != id {
			t.Fatal("omission changed full declared input contract", i, call.Args[i].ResultUsage())
		}
	}
	for slot, want := range map[int]string{2: "{}", 3: "f"} {
		value, err := call.Args[slot].Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil || value.IsNull() {
			t.Fatal("default is not a real nonnull child", slot, value, err)
		}
		text, err := value.Type().Output(ctx, value)
		if err != nil || text != want {
			t.Fatal("released default value", slot, text, err)
		}
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(binderOperation(b))
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
	checks := append([]catalog.ExpressionDependency(nil), b.dependencies...)
	for i := range checks {
		if checks[i].Ref.Address == address {
			checks[i].Revision++
		}
	}
	if err := admitRoutineDescriptor(auth, checks, captured, nil); err == nil {
		t.Fatal("stale full-signature routine lifetime admitted")
	}
}
