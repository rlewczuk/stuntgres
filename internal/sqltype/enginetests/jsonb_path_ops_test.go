package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestJSONBPathOpsBinding(t *testing.T) {
	for _, tc := range []struct {
		op, name string
		oid      ids.OID
	}{
		{"@?", "jsonb_path_exists_opr", 4010}, {"@@", "jsonb_path_match_opr", 4011},
	} {
		t.Run(tc.op, func(t *testing.T) {
			e := engine.New()
			ctx := context.Background()
			b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			statements, err := parser.Parse(`SELECT 'true'::jsonb OPERATOR(pg_catalog.` + tc.op + `) '$'::jsonpath`)
			if err != nil {
				t.Fatal(err)
			}
			raw, ok := statements[0].(*parser.Select).Targets[0].(parser.OperatorExpr)
			if !ok {
				t.Fatal("not an operator")
			}
			call, err := b.BindCatalogOperator(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			binary, ok := call.(functions.Binary)
			if !ok || len(binary.Args) != 2 || binary.ResultUsage().Type.ID() != sqltype.Bool || binary.Properties().Volatility != expr.Immutable || binary.LooksLikeFunction() {
				t.Fatal("selected scalar contract", call)
			}
			captured, err := exec.ExpressionRoutineDescriptor(call)
			if err != nil || captured == nil || captured.Builtin == nil {
				t.Fatal("missing executable authority", captured, err)
			}
			identity := catalog.RoutineIdentity{tc.oid, ids.PGCatalogNamespaceOID, tc.name, "3802,4072"}
			d := captured.Builtin
			if d.Identity != identity || d.Kind != catalog.FunctionRoutine || d.Result != 16 || !d.Strict || d.Volatility != expr.Immutable || d.Parallel != 's' || d.ReturnsSet || d.Defaults != 0 || d.Variadic != 0 || d.Providers != functions.OperatorRoutineProvider {
				t.Fatal("released full declaration", d)
			}
			operatorID := ids.OID(4012)
			if tc.op == "@@" {
				operatorID = 4013
			}
			operator := e.CatalogRead().MustOperator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: operatorID})
			if operator.Left != 3802 || operator.Right != 4072 || operator.Result != 16 ||
				operator.Function != tc.oid || operator.Restriction != 5040 || operator.Join != 5041 ||
				operator.Commutator != 0 || operator.Negator != 0 || operator.Hash || operator.Merge {
				t.Fatal("incomplete released operator declaration", operator)
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(binderOperation(b))
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err := exec.AdmitRoutineDescriptor(core.RoutineAccessOf(auth), b.Dependencies, captured, nil); err != nil {
				t.Fatal("actual admission", err)
			}
			if err := exec.AdmitRoutineDescriptor(core.RoutineAccessOf(auth), nil, captured, nil); err == nil {
				t.Fatal("missing dependency repaired")
			}
			stale := append([]catalog.ExpressionDependency(nil), b.Dependencies...)
			for i := range stale {
				if stale[i].Ref.Address == captured.Address() {
					stale[i].Revision++
				}
			}
			if err := exec.AdmitRoutineDescriptor(core.RoutineAccessOf(auth), stale, captured, nil); err == nil {
				t.Fatal("stale declaration admitted")
			}
			// Reuse the actual routine through the existing custom-operator seam.
			alias, err := b.BindSelectedOperatorRoutine(captured, binary.Args, raw.Pos)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := exec.ExpressionRoutineDescriptor(alias)
			if err != nil || actual != captured {
				t.Fatal("alias lost genuine provider", actual, err)
			}
		})
	}
}
