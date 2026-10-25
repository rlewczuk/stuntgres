package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestTimestamptzSeriesRoutineAuthority(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	other, otherOp, otherScope := strictNullUnitFixture(t)
	_, _, foreign := strictNullUnitBind(t, other, otherOp, otherScope, "1")
	for _, tc := range []struct {
		expression, signature string
		oid                   ids.OID
		arity                 int
		volatility            expr.Volatility
	}{
		{"generate_series(TIMESTAMPTZ '2024-01-01+00',TIMESTAMPTZ '2024-01-02+00',INTERVAL '1 day')", "1184,1184,1186", 939, 3, expr.Stable},
		{"generate_series(NULL::timestamptz,TIMESTAMPTZ '2024-01-02+00',INTERVAL '1 day')", "1184,1184,1186", 939, 3, expr.Stable},
		{"generate_series(TIMESTAMPTZ '2024-01-01+00',TIMESTAMPTZ '2024-01-02+00',INTERVAL '1 day','UTC')", "1184,1184,1186,25", 6274, 4, expr.Immutable},
		{"generate_series(TIMESTAMPTZ '2024-01-01+00',TIMESTAMPTZ '2024-01-02+00',INTERVAL '1 day',NULL::text)", "1184,1184,1186,25", 6274, 4, expr.Immutable},
	} {
		node, binder, evaluation := strictNullUnitBind(t, e, op, scope, tc.expression)
		s := node.(plan.SeriesExpr)
		captured, err := exec.ExpressionRoutineDescriptor(node)
		if err != nil || captured == nil || captured.Builtin == nil {
			t.Fatal(captured, err)
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: tc.oid}
		declaration := captured.Builtin
		if captured.Address() != address || captured.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}) || captured.Name() != "generate_series" || string(captured.Signature) != tc.signature || captured.Result != 1184 || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != tc.volatility || captured.Revision() == 0 || len(captured.Body) != 0 {
			t.Fatal("selected release identity", captured)
		}
		if declaration.Kind != catalog.FunctionRoutine || declaration.Result != 1184 || !declaration.Strict || declaration.Volatility != tc.volatility || declaration.Parallel != 's' || !declaration.ReturnsSet || declaration.Variadic != 0 || declaration.Defaults != 0 || declaration.Providers&functions.SeriesRoutineProvider == 0 {
			t.Fatal("selected declaration flags", declaration)
		}
		if s.ResultUsage().Type.ID() != sqltype.Timestamptz || s.ResultUsage() != s.ResultUsage().Type.Usage() || len(s.Args) != tc.arity || s.IntrinsicVolatility() != tc.volatility || !exec.SourceAdmitted(s) || !binder.Clause.QueryHasTargetSet {
			t.Fatal("selected carrier", s)
		}
		owned, ok := evaluation.Catalog.Procedure(address)
		if !ok || owned != captured {
			t.Fatal("lost owned descriptor")
		}
		original := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
		found := false
		for _, dependency := range original {
			if dependency.Ref.Address == address {
				found = true
				if dependency.Revision != captured.Revision() || dependency.Ref.Epoch != evaluation.Catalog.Epoch() || dependency.Ref.Domain() != e.Catalog.Domain {
					t.Fatal("original manifest", dependency)
				}
			}
		}
		if !found {
			t.Fatal("missing selected dependency")
		}
		if err := exec.AdmitRoutineCall(&evaluation, s, false, false); err != nil {
			t.Fatal("original admission", err)
		}
		missing := make([]catalog.ExpressionDependency, 0, len(original))
		for _, dependency := range original {
			if dependency.Ref.Address != address {
				missing = append(missing, dependency)
			}
		}
		for _, manifest := range [][]catalog.ExpressionDependency{nil, {}, missing} {
			probe := evaluation
			probe.RoutineChecks = manifest
			if err := exec.AdmitRoutineCall(&probe, s, false, false); err == nil {
				t.Fatal("repaired absent original authority")
			}
		}
		for _, epoch := range []bool{false, true} {
			stale := append([]catalog.ExpressionDependency(nil), original...)
			for i := range stale {
				if stale[i].Ref.Address == address {
					if epoch {
						stale[i].Ref.Epoch++
					} else {
						stale[i].Revision++
					}
				}
			}
			probe := evaluation
			probe.RoutineChecks = stale
			if err := exec.AdmitRoutineCall(&probe, s, false, false); err == nil {
				t.Fatal("admitted stale original authority")
			}
		}
		probe := evaluation
		foreignAuthority := *RoutineAuthorityView(&foreign)
		probe.RoutineAuthority = &foreignAuthority
		if err := exec.AdmitRoutineCall(&probe, s, false, false); err == nil {
			t.Fatal("foreign equal-OID authority")
		}
		changed := *RoutineAuthorityView(&evaluation)
		changed.Catalog = changed.Catalog.WithState(catalog.CloneState(changed.Catalog.State()))
		replacement := *captured
		replacement.Volatility = expr.Volatile
		changed.Catalog.State().SetDescriptor(address, &replacement)
		probe = evaluation
		probe.RoutineAuthority = &changed
		if err := exec.AdmitRoutineCall(&probe, s, false, false); err == nil {
			t.Fatal("incompatible declaration replacement")
		}
		text, err := (stored.Deparser{View: e.CatalogRead()}).SQL(context.Background(), s, stored.DeparseParent{})
		if err != nil {
			t.Fatal(err)
		}
		rebound, _, reboundEvaluation := strictNullUnitBind(t, e, op, scope, text)
		reboundDescriptor, err := exec.ExpressionRoutineDescriptor(rebound)
		if err != nil || reboundDescriptor != captured || len(rebound.(plan.SeriesExpr).Args) != tc.arity {
			t.Fatal("deparse identity/arity", reboundDescriptor, err)
		}
		if err := exec.AdmitRoutineCall(&reboundEvaluation, rebound, false, false); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(binder.Dependencies, original) {
			t.Fatal("mutated authority")
		}
		if err := exec.AdmitRoutineCall(&evaluation, s, false, false); err != nil {
			t.Fatal("lost original authority after probes", err)
		}
	}
}
