package functions_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// These tests exercise the actual bound carrier and its original manifest,
// not public SQL answers or standalone Eval as a substitute for admission.
func TestObjectAddressRoutineOriginalAuthority(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	other, otherOp, otherScope := strictNullUnitFixture(t)
	_, _, foreign := strictNullUnitBind(t, other, otherOp, otherScope, "1")
	for _, tc := range []struct {
		name, expression, signature string
		oid, result                 ids.OID
		slots                       [3]uint8
	}{
		{"pg_identify_object", "pg_identify_object(objsubid=>0,objid=>23,classid=>1247)", "26,26,23", 3839, 2249, [3]uint8{2, 1, 0}},
		{"pg_identify_object_as_address", "pg_identify_object_as_address(objsubid=>0,objid=>23,classid=>1247)", "26,26,23", 3382, 2249, [3]uint8{2, 1, 0}},
		{"pg_get_object_address", "pg_get_object_address(object_args=>'{}',type=>'type',object_names=>'{integer}')", "25,1009,1009", 3954, 2249, [3]uint8{2, 0, 1}},
		{"pg_describe_object", "pg_describe_object(1247,23,0)", "26,26,23", 3537, 25, [3]uint8{0, 1, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, tc.expression)
			carrier, ok := node.(functions.ObjectAddress)
			if !ok || carrier.Slots != tc.slots || carrier.Validate() != nil {
				t.Fatal("canonical named-slot carrier", node)
			}
			captured, err := exec.ExpressionRoutineDescriptor(node)
			if err != nil || captured == nil {
				t.Fatal("missing genuine routine reference", captured, err)
			}
			address := ids.ObjectAddress{ids.ProcedureClassID, tc.oid, 0}
			if captured.Address() != address || captured.Namespace() != (ids.ObjectAddress{ids.NamespaceClassID, 11, 0}) || captured.Name() != tc.name || string(captured.Signature) != tc.signature || captured.Result != tc.result || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Stable || captured.Revision() == 0 || len(captured.Body) != 0 {
				t.Fatal("independent released declaration", captured)
			}
			if carrier.Properties().Volatility != expr.Stable || plan.GenerationImmutable(carrier) {
				t.Fatal("address inquiry lost STABLE boundary")
			}
			owned, ok := evaluation.Catalog.Procedure(address)
			if !ok || owned != captured {
				t.Fatal("reference is not the original owned descriptor")
			}
			original := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
			found := false
			for _, dependency := range original {
				if dependency.Ref.Address == address {
					found = true
					if dependency.Revision != captured.Revision() || dependency.Ref.Epoch != evaluation.Catalog.Epoch() || dependency.Ref.Domain() != e.Catalog.Domain {
						t.Fatal("original canonical dependency", dependency)
					}
				}
			}
			if !found {
				t.Fatal("canonical callable absent from original manifest")
			}
			before := carrier
			if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
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
				if err := exec.AdmitRoutineCall(&probe, node, true, false); err == nil {
					t.Fatal("missing original manifest acquired authority from an OID")
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
				if err := exec.AdmitRoutineCall(&probe, node, true, false); err == nil {
					t.Fatal("stale original epoch/revision admitted")
				}
			}
			probe := evaluation
			foreignAuth := *core.RoutineAuthorityView(&foreign)
			probe.RoutineAuthority = &foreignAuth
			if err := exec.AdmitRoutineCall(&probe, node, true, false); err == nil {
				t.Fatal("foreign original descriptor admitted")
			}
			remapped, _, err := stored.RetagTypes(other.CatalogRead(), node, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				if old == nil {
					return nil
				}
				current, _ := other.CatalogRead().LookupType(old.ID())
				return current
			})
			if err != nil {
				t.Fatal("actual carrier retag", err)
			}
			if err := exec.AdmitRoutineCall(&probe, remapped, true, false); err == nil {
				t.Fatal("foreign retag manufactured an original manifest")
			}
			// A catalog-local replacement with an incompatible declaration is
			// not rescued by an equal numeric address either.
			changed := *core.RoutineAuthorityView(&evaluation)
			changed.Catalog = changed.Catalog.WithState(catalog.CloneState(changed.Catalog.State()))
			replacement := *captured
			replacement.Strict = false
			changed.Catalog.State().SetDescriptor(address, &replacement)
			probe = evaluation
			probe.RoutineAuthority = &changed
			if err := exec.AdmitRoutineCall(&probe, node, true, false); err == nil {
				t.Fatal("changed selected declaration admitted")
			}
			if !reflect.DeepEqual(carrier, before) || !reflect.DeepEqual(binder.Dependencies, original) {
				t.Fatal("negative probes mutated original carrier/manifest")
			}
			if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
				t.Fatal("original authority lost after negative probes", err)
			}
		})
	}
}
