package engine

import (
	"context"
	"reflect"
	"testing"

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
		oid                         OID
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
			if !ok || len(carrier.args) != tc.arity || !validFunctionSourceCall(carrier) || !binder.queryHasTargetSet {
				t.Fatal("original SRF arity/source/project carrier", node)
			}
			captured, err := expressionRoutineDescriptor(node)
			if err != nil || captured == nil || captured.builtin == nil {
				t.Fatal("missing genuine selected declaration", captured, err)
			}
			address := ObjectAddress{ProcedureClassID, tc.oid, 0}
			declaration := captured.builtin
			if captured.address != address || captured.namespace != (ObjectAddress{NamespaceClassID, 11, 0}) || captured.name != "generate_series" || string(captured.signature) != tc.signature || captured.result != 1700 || captured.routineKind != functionRoutine || !captured.strict || captured.volatility != Immutable || captured.revision == 0 || len(captured.body) != 0 {
				t.Fatal("independent released identity", captured)
			}
			if declaration.kind != functionRoutine || declaration.result != 1700 || !declaration.strict || declaration.volatility != Immutable || declaration.parallel != 's' || !declaration.returnsSet || declaration.variadic != 0 || declaration.defaults != 0 || declaration.providers&seriesRoutineProvider == 0 {
				t.Fatal("independent released declaration flags", declaration)
			}
			if carrier.ResultUsage().Type.ID() != sqltype.Numeric || carrier.ResultUsage() != carrier.ResultUsage().Type.Usage() || carrier.Properties().Volatility != Immutable {
				t.Fatal("unconstrained immutable NUMERIC result", carrier)
			}
			owned, ok := evaluation.catalog.state.descriptors[address].(*procedureDescriptor)
			if !ok || owned != captured {
				t.Fatal("call did not capture original owned descriptor")
			}
			original := append([]expressionDependency(nil), binder.dependencies...)
			found := false
			for _, dependency := range original {
				if dependency.ref.Address == address {
					found = true
					if dependency.revision != captured.revision || dependency.ref.Epoch != evaluation.catalog.Epoch() || dependency.ref.owner != e.domain {
						t.Fatal("original canonical ref/revision", dependency)
					}
				} else if dependency.ref.Address.ClassID == ProcedureClassID {
					t.Fatal("numeric series invented a SQL helper/sibling dependency", dependency)
				}
			}
			if !found {
				t.Fatal("selected declaration absent from original manifest")
			}
			before := carrier
			before.args = append([]BoundExpr(nil), carrier.args...)
			// false is the real source/project admission mode. Scalar planning
			// intentionally skips SRFs and cannot prove any negative below.
			if err := admitRoutineCall(&evaluation, node, false, false); err != nil {
				t.Fatal("original source/project admission", err)
			}
			missing := make([]expressionDependency, 0, len(original))
			for _, dependency := range original {
				if dependency.ref.Address != address {
					missing = append(missing, dependency)
				}
			}
			for _, manifest := range [][]expressionDependency{nil, {}, missing} {
				probe := evaluation
				probe.routineChecks = manifest
				if err := admitRoutineCall(&probe, node, false, false); err == nil {
					t.Fatal("source/project admission repaired absent original authority from OID")
				}
			}
			for _, epoch := range []bool{false, true} {
				stale := append([]expressionDependency(nil), original...)
				for i := range stale {
					if stale[i].ref.Address == address {
						if epoch {
							stale[i].ref.Epoch++
						} else {
							stale[i].revision++
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
			changed.catalog.state = cloneCatalog(changed.catalog.state)
			replacement := *captured
			replacement.strict = false
			changed.catalog.state.descriptors[address] = &replacement
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
