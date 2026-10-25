package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// These are provider-carrier/lifetime tests, not duplicate public SQL answers.
// The release identities are fixed here independently of the native inventory.
func TestNumericSeriesRoutineOriginalAuthority(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	other, otherOp, otherScope := strictNullUnitFixture(t)
	_, _, foreign := strictNullUnitBind(t, other, otherOp, otherScope, "1")
	for _, tc := range []struct {
		name, expression, signature string
		oid                         ids.OID
		arity                       int
	}{
		{"short", "generate_series(0.10::numeric,2.10::numeric)", "1700,1700", 3260, 2},
		{"long", "generate_series(0.1::numeric,0.3::numeric,0.1::numeric)", "1700,1700,1700", 3259, 3},
		{"short_null", "generate_series(NULL::numeric,2.10::numeric)", "1700,1700", 3260, 2},
		{"long_null", "generate_series(0.1::numeric,0.3::numeric,NULL::numeric)", "1700,1700,1700", 3259, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, tc.expression)
			carrier, ok := node.(plan.SeriesExpr)
			if !ok || len(carrier.Args) != tc.arity || !exec.SourceAdmitted(carrier) || !binder.Clause.QueryHasTargetSet {
				t.Fatal("original SRF arity/source/project carrier", node)
			}
			captured, err := exec.ExpressionRoutineDescriptor(node)
			if err != nil || captured == nil || captured.Builtin == nil {
				t.Fatal("missing genuine selected declaration", captured, err)
			}
			address := ids.ObjectAddress{ids.ProcedureClassID, tc.oid, 0}
			declaration := captured.Builtin
			if captured.Address() != address || captured.Namespace() != (ids.ObjectAddress{ids.NamespaceClassID, 11, 0}) || captured.Name() != "generate_series" || string(captured.Signature) != tc.signature || captured.Result != 1700 || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Immutable || captured.Revision() == 0 || len(captured.Body) != 0 {
				t.Fatal("independent released identity", captured)
			}
			if declaration.Kind != catalog.FunctionRoutine || declaration.Result != 1700 || !declaration.Strict || declaration.Volatility != expr.Immutable || declaration.Parallel != 's' || !declaration.ReturnsSet || declaration.Variadic != 0 || declaration.Defaults != 0 || declaration.Providers&functions.SeriesRoutineProvider == 0 {
				t.Fatal("independent released declaration flags", declaration)
			}
			if carrier.ResultUsage().Type.ID() != sqltype.Numeric || carrier.ResultUsage() != carrier.ResultUsage().Type.Usage() || carrier.Properties().Volatility != expr.Immutable {
				t.Fatal("unconstrained immutable NUMERIC result", carrier)
			}
			owned, ok := evaluation.Catalog.Procedure(address)
			if !ok || owned != captured {
				t.Fatal("call did not capture original owned descriptor")
			}
			original := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
			found := false
			for _, dependency := range original {
				if dependency.Ref.Address == address {
					found = true
					if dependency.Revision != captured.Revision() || dependency.Ref.Epoch != evaluation.Catalog.Epoch() || dependency.Ref.Domain() != e.Catalog.Domain {
						t.Fatal("original canonical ref/revision", dependency)
					}
				} else if dependency.Ref.Address.ClassID == ids.ProcedureClassID {
					t.Fatal("numeric series invented a SQL helper/sibling dependency", dependency)
				}
			}
			if !found {
				t.Fatal("selected declaration absent from original manifest")
			}
			before := carrier
			before.Args = append([]expr.Expr(nil), carrier.Args...)
			// false is the real source/project admission mode. Scalar planning
			// intentionally skips SRFs and cannot prove any negative below.
			if err := exec.AdmitRoutineCall(&evaluation, node, false, false); err != nil {
				t.Fatal("original source/project admission", err)
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
				if err := exec.AdmitRoutineCall(&probe, node, false, false); err == nil {
					t.Fatal("source/project admission repaired absent original authority from OID")
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
				if err := exec.AdmitRoutineCall(&probe, node, false, false); err == nil {
					t.Fatal("stale original source/project manifest admitted")
				}
			}
			probe := evaluation
			foreignAuth := *RoutineAuthorityView(&foreign)
			probe.RoutineAuthority = &foreignAuth
			if err := exec.AdmitRoutineCall(&probe, node, false, false); err == nil {
				t.Fatal("foreign equal-OID authority admitted")
			}
			remapped, _, err := stored.RetagTypes(other.CatalogRead(), node, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				if old == nil {
					return nil
				}
				current, _ := other.CatalogRead().LookupType(old.ID())
				return current
			})
			if err != nil {
				t.Fatal("actual numeric carrier retag", err)
			}
			if len(remapped.(plan.SeriesExpr).Args) != tc.arity {
				t.Fatal("retag expanded omitted step into a third formal")
			}
			if err := exec.AdmitRoutineCall(&probe, remapped, false, false); err == nil {
				t.Fatal("foreign retag manufactured original authority")
			}
			changed := *RoutineAuthorityView(&evaluation)
			changed.Catalog = changed.Catalog.WithState(catalog.CloneState(changed.Catalog.State()))
			replacement := *captured
			replacement.Strict = false
			changed.Catalog.State().SetDescriptor(address, &replacement)
			probe = evaluation
			probe.RoutineAuthority = &changed
			if err := exec.AdmitRoutineCall(&probe, node, false, false); err == nil {
				t.Fatal("incompatible selected declaration replacement admitted")
			}
			// Rendering/rebinding must preserve the short declaration, not emit a
			// private synthesized step that changes the original callable identity.
			text, err := (stored.Deparser{View: e.CatalogRead()}).SQL(context.Background(), node, stored.DeparseParent{})
			if err != nil {
				t.Fatal("render original carrier", err)
			}
			rebound, _, reboundEvaluation := strictNullUnitBind(t, e, op, scope, text)
			reboundDescriptor, err := exec.ExpressionRoutineDescriptor(rebound)
			if err != nil || reboundDescriptor != captured || len(rebound.(plan.SeriesExpr).Args) != tc.arity {
				t.Fatal("render/rebind lost original full-IN declaration", reboundDescriptor, err)
			}
			if err := exec.AdmitRoutineCall(&reboundEvaluation, rebound, false, false); err != nil {
				t.Fatal("genuine rebound source/project admission", err)
			}
			if !reflect.DeepEqual(carrier, before) || !reflect.DeepEqual(binder.Dependencies, original) {
				t.Fatal("probes mutated original args/info/reference/manifest")
			}
			if err := exec.AdmitRoutineCall(&evaluation, node, false, false); err != nil {
				t.Fatal("original authority lost after probes", err)
			}
		})
	}
}
