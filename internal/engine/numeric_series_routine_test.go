package engine

import (
	"context"
	"reflect"
	"testing"

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
			carrier, ok := node.(seriesExpression)
			if !ok || len(carrier.args) != tc.arity || !sourceAdmitted(carrier) || !binder.queryHasTargetSet {
				t.Fatal("original SRF arity/source/project carrier", node)
			}
			captured, err := expressionRoutineDescriptor(node)
			if err != nil || captured == nil || captured.Builtin == nil {
				t.Fatal("missing genuine selected declaration", captured, err)
			}
			address := ids.ObjectAddress{ids.ProcedureClassID, tc.oid, 0}
			declaration := captured.Builtin
			if captured.Address() != address || captured.Namespace() != (ids.ObjectAddress{ids.NamespaceClassID, 11, 0}) || captured.Name() != "generate_series" || string(captured.Signature) != tc.signature || captured.Result != 1700 || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Immutable || captured.Revision() == 0 || len(captured.Body) != 0 {
				t.Fatal("independent released identity", captured)
			}
			if declaration.Kind != catalog.FunctionRoutine || declaration.Result != 1700 || !declaration.Strict || declaration.Volatility != expr.Immutable || declaration.Parallel != 's' || !declaration.ReturnsSet || declaration.Variadic != 0 || declaration.Defaults != 0 || declaration.Providers&seriesRoutineProvider == 0 {
				t.Fatal("independent released declaration flags", declaration)
			}
			if carrier.ResultUsage().Type.ID() != sqltype.Numeric || carrier.ResultUsage() != carrier.ResultUsage().Type.Usage() || carrier.Properties().Volatility != expr.Immutable {
				t.Fatal("unconstrained immutable NUMERIC result", carrier)
			}
			owned, ok := evaluation.catalog.Procedure(address)
			if !ok || owned != captured {
				t.Fatal("call did not capture original owned descriptor")
			}
			original := append([]catalog.ExpressionDependency(nil), binder.dependencies...)
			found := false
			for _, dependency := range original {
				if dependency.Ref.Address == address {
					found = true
					if dependency.Revision != captured.Revision() || dependency.Ref.Epoch != evaluation.catalog.Epoch() || dependency.Ref.Domain() != e.catalog.Domain {
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
			before.args = append([]expr.Expr(nil), carrier.args...)
			// false is the real source/project admission mode. Scalar planning
			// intentionally skips SRFs and cannot prove any negative below.
			if err := admitRoutineCall(&evaluation, node, false, false); err != nil {
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
				probe.routineChecks = manifest
				if err := admitRoutineCall(&probe, node, false, false); err == nil {
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
				probe.routineChecks = stale
				if err := admitRoutineCall(&probe, node, false, false); err == nil {
					t.Fatal("stale original source/project manifest admitted")
				}
			}
			probe := evaluation
			foreignAuth := *foreign.routineAuthority
			probe.routineAuthority = &foreignAuth
			if err := admitRoutineCall(&probe, node, false, false); err == nil {
				t.Fatal("foreign equal-OID authority admitted")
			}
			remapped, _, err := retagBoundTypes(other.CatalogRead(), node, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				if old == nil {
					return nil
				}
				current, _ := other.CatalogRead().LookupType(old.ID())
				return current
			})
			if err != nil {
				t.Fatal("actual numeric carrier retag", err)
			}
			if len(remapped.(seriesExpression).args) != tc.arity {
				t.Fatal("retag expanded omitted step into a third formal")
			}
			if err := admitRoutineCall(&probe, remapped, false, false); err == nil {
				t.Fatal("foreign retag manufactured original authority")
			}
			changed := *evaluation.routineAuthority
			changed.Catalog = changed.Catalog.WithState(catalog.CloneState(changed.Catalog.State()))
			replacement := *captured
			replacement.Strict = false
			changed.Catalog.State().SetDescriptor(address, &replacement)
			probe = evaluation
			probe.routineAuthority = &changed
			if err := admitRoutineCall(&probe, node, false, false); err == nil {
				t.Fatal("incompatible selected declaration replacement admitted")
			}
			// Rendering/rebinding must preserve the short declaration, not emit a
			// private synthesized step that changes the original callable identity.
			text, err := (pgRenderer{view: e.CatalogRead()}).sql(context.Background(), node, pgRenderParent{})
			if err != nil {
				t.Fatal("render original carrier", err)
			}
			rebound, _, reboundEvaluation := strictNullUnitBind(t, e, op, scope, text)
			reboundDescriptor, err := expressionRoutineDescriptor(rebound)
			if err != nil || reboundDescriptor != captured || len(rebound.(seriesExpression).args) != tc.arity {
				t.Fatal("render/rebind lost original full-IN declaration", reboundDescriptor, err)
			}
			if err := admitRoutineCall(&reboundEvaluation, rebound, false, false); err != nil {
				t.Fatal("genuine rebound source/project admission", err)
			}
			if !reflect.DeepEqual(carrier, before) || !reflect.DeepEqual(binder.dependencies, original) {
				t.Fatal("probes mutated original args/info/reference/manifest")
			}
			if err := admitRoutineCall(&evaluation, node, false, false); err != nil {
				t.Fatal("original authority lost after probes", err)
			}
		})
	}
}
