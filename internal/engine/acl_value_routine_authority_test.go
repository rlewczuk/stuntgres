package engine

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Released identities are independent of the production inventory. These probes
// exercise actual bound carriers and original manifests, not public SQL results.
func TestACLValueRoutineOriginalAuthority(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	other, otherOp, otherScope := strictNullUnitFixture(t)
	_, _, foreign := strictNullUnitBind(t, other, otherOp, otherScope, "1")
	for _, tc := range []struct {
		name, expression, signature string
		oid, result                 ids.OID
		kind                        aclValueKind
		volatility                  expr.Volatility
		set                         bool
		provider                    routineProvider
		formals                     []sqltype.ID
	}{
		{"acldefault", "acldefault('r',$1::oid)", "18,26", 3943, 1034, aclDefault, expr.Immutable, false, catalogRoutineProvider, []sqltype.ID{18, 26}},
		{"makeaclitem", "makeaclitem($1::oid,$2::oid,$3::text,$4::boolean)", "26,26,25,16", 1365, 1033, aclMake, expr.Immutable, false, catalogRoutineProvider, []sqltype.ID{26, 26, 25, 16}},
		{"aclexplode", "aclexplode(acl => $1::aclitem[])", "1034", 1689, 2249, aclExplode, expr.Stable, true, catalogRoutineProvider, []sqltype.ID{1034}},
		{"aclcontains", "$1::aclitem[] @> $2::aclitem", "1034,1033", 1037, 16, aclContains, expr.Immutable, false, operatorRoutineProvider, []sqltype.ID{1034, 1033}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, tc.expression)
			call, ok := node.(aclValueExpression)
			if !ok || call.kind != tc.kind || len(call.arguments()) != len(tc.formals) {
				t.Fatal("actual ACL carrier/full IN arity", node)
			}
			for i, formal := range tc.formals {
				if call.args[i].ResultUsage().Type.ID() != formal {
					t.Fatal("physical coerced IN type", i, call.args[i].ResultUsage(), formal)
				}
			}
			if got := ids.OID(call.ResultUsage().Type.ID()); got != tc.result {
				t.Fatalf("physical result ID: got %d, want %d", got, tc.result)
			}
			if got := call.Properties().Volatility; got != tc.volatility {
				t.Fatalf("physical volatility: got %v, want %v", got, tc.volatility)
			}
			if got := isQuerySet(call); got != tc.set {
				t.Fatalf("isQuerySet: got %t, want %t", got, tc.set)
			}
			// Containment is deliberately not a function-source capability.
			if got, want := validFunctionSourceCall(call), tc.kind != aclContains; got != want {
				t.Fatalf("validFunctionSourceCall: got %t, want %t", got, want)
			}
			// bindACLValue returns the bare carrier directly, without setting
			// the binder's target-set observation. Actual set classification
			// above and source/project admission below own this SRF contract.
			if binder.queryHasTargetSet {
				t.Fatalf("bare ACL binder queryHasTargetSet: got %t, want false", binder.queryHasTargetSet)
			}
			if tc.set {
				shape := call.ResultUsage().Type
				if !call.named || shape.CompositeLen() != 4 {
					t.Fatal("named sole IN/four OUT shape", call)
				}
				for i, out := range []struct {
					name string
					typ  sqltype.ID
				}{{"grantor", 26}, {"grantee", 26}, {"privilege_type", 25}, {"is_grantable", 16}} {
					member := shape.CompositeMember(i)
					if member.Attribute != int32(i+1) || member.Name != out.name || member.Usage.Type.ID() != out.typ {
						t.Fatal("independent OUT record layout", i, member, out)
					}
				}
			}
			captured, err := expressionRoutineDescriptor(call)
			if err != nil || captured == nil || captured.Builtin == nil {
				t.Fatal("missing selected original declaration", captured, err)
			}
			address := ids.ObjectAddress{ids.ProcedureClassID, tc.oid, 0}
			declaration := captured.Builtin
			identity := routineIdentity{tc.oid, ids.PGCatalogNamespaceOID, tc.name, procedureSignature(tc.signature)}
			if declaration.Identity != identity || declaration.Kind != functionRoutine || declaration.Result != tc.result || !declaration.Strict || declaration.Volatility != tc.volatility || declaration.Parallel != 's' || declaration.ReturnsSet != tc.set || declaration.Defaults != 0 || declaration.Variadic != 0 || declaration.Providers != tc.provider {
				t.Fatal("independent released full-IN declaration", declaration)
			}
			if captured.Address() != address || captured.Namespace() != (ids.ObjectAddress{ids.NamespaceClassID, 11, 0}) || captured.Name() != tc.name || string(captured.Signature) != tc.signature || captured.Result != tc.result || captured.RoutineKind != functionRoutine || !captured.Strict || captured.Volatility != tc.volatility || captured.Revision() == 0 || len(captured.Body) != 0 || evaluation.catalog.Get(address) != captured {
				t.Fatal("selected owned descriptor/no SQL helper body", captured)
			}
			original := append([]expressionDependency(nil), binder.dependencies...)
			selected := 0
			var missing []expressionDependency
			for _, dependency := range original {
				if dependency.Ref.Address == address {
					selected++
					if dependency.Ref.Domain() != e.catalog.Domain || dependency.Ref.Epoch != evaluation.catalog.Epoch() || dependency.Revision != captured.Revision() {
						t.Fatal("original selected lifetime", dependency)
					}
				} else {
					missing = append(missing, dependency)
					if dependency.Ref.Address.ClassID == ids.ProcedureClassID {
						t.Fatal("implicit constructor/codec/helper EXECUTE edge", dependency)
					}
				}
			}
			if selected != 1 {
				t.Fatal("selected original dependency multiplicity", selected)
			}
			// In particular, aclexplode must pass actual source/project admission;
			// scalar planning intentionally skips SRFs and cannot prove authority.
			if err := admitRoutineCall(&evaluation, call, false, false); err != nil {
				t.Fatal("original source/project authority", err)
			}
			for _, manifest := range [][]expressionDependency{nil, {}, missing} {
				probe := evaluation
				probe.routineChecks = manifest
				if err := admitRoutineCall(&probe, call, false, false); err == nil {
					t.Fatal("missing original lifetime repaired from current OID")
				}
			}
			for _, mode := range []string{"foreign", "epoch", "revision"} {
				checks := append([]expressionDependency(nil), original...)
				for i := range checks {
					if checks[i].Ref.Address == address {
						switch mode {
						case "foreign":
							checks[i].Ref = other.catalog.Domain.Ref(checks[i].Ref.Epoch, checks[i].Ref.Address)
						case "epoch":
							checks[i].Ref.Epoch++
						case "revision":
							checks[i].Revision++
						}
					}
				}
				probe := evaluation
				probe.routineChecks = checks
				if err := admitRoutineCall(&probe, call, false, false); err == nil {
					t.Fatal("invalid selected original lifetime admitted", mode)
				}
			}
			probe := evaluation
			probe.routineAuthority = foreign.routineAuthority
			if err := admitRoutineCall(&probe, call, false, false); err == nil {
				t.Fatal("equal-OID foreign authority admitted")
			}
			remapped, _, err := retagBoundTypes(other.CatalogRead(), call, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				if old == nil || old.ID() == 2249 {
					return old
				}
				current, _ := other.CatalogRead().LookupType(old.ID())
				return current
			})
			if err != nil {
				t.Fatal("actual ACL carrier retag", err)
			}
			mapped := remapped.(aclValueExpression)
			mappedDescriptor, err := expressionRoutineDescriptor(mapped)
			if err != nil || mappedDescriptor == captured || mappedDescriptor != foreign.catalog.Get(address) || mapped.kind != call.kind || mapped.named != call.named || mapped.namespace != call.namespace || mapped.SourcePosition() != call.SourcePosition() || mapped.Properties() != call.Properties() {
				t.Fatal("retag lost selected carrier contract", mapped, err)
			}
			for _, manifest := range [][]expressionDependency{original, nil, {}} {
				probe.routineChecks = manifest
				if err := admitRoutineCall(&probe, mapped, false, false); err == nil {
					t.Fatal("retag manufactured original destination authority")
				}
			}
			changed := *evaluation.routineAuthority
			changed.catalog.state = cloneCatalog(changed.catalog.state)
			replacement := *captured
			replacement.Strict = false
			changed.catalog.state.setDescriptor(address, &replacement)
			probe = evaluation
			probe.routineAuthority = &changed
			if err := admitRoutineCall(&probe, call, false, false); err == nil {
				t.Fatal("incompatible current declaration admitted")
			}
			invalid := call
			invalid.RoutineReference = (*procedureDescriptor)(nil)
			if err := admitRoutineCall(&evaluation, invalid, false, false); err == nil {
				t.Fatal("typed-nil selected descriptor admitted")
			}
			if !reflect.DeepEqual(binder.dependencies, original) {
				t.Fatal("negative probes mutated original manifest")
			}
			if err := admitRoutineCall(&evaluation, call, false, false); err != nil {
				t.Fatal("intact original authority changed after probes", err)
			}
		})
	}
}
