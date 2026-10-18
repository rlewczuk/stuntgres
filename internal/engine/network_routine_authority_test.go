package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// These are independently enumerated REL_18_0 declarations, not expected
// answers obtained from the native network kernel or its association table.
func TestNetworkOperatorRoutineOriginalAuthority(t *testing.T) {
	for _, test := range []struct {
		expression, name, formals string
		id, result                ids.OID
	}{
		{"~ip", "inetnot", "869", 2627, 869},
		{"ip&ip", "inetand", "869,869", 2628, 869},
		{"ip|ip", "inetor", "869,869", 2629, 869},
		{"ip+n8", "inetpl", "869,20", 2630, 869},
		{"n8+ip", "int8pl_inet", "20,869", 2631, 869},
		{"ip-n8", "inetmi_int8", "869,20", 2632, 869},
		{"ip-ip", "inetmi", "869,869", 2633, 20},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := New()
			admin, finish := roleOperation(t, e, "postgres")
			defer finish()
			roleExecute(t, e, admin, "CREATE ROLE network_original_reader LOGIN")
			roleExecute(t, e, admin, "CREATE TABLE network_inputs(ip inet,n8 bigint)")
			reader, readerDone := roleOperation(t, e, "network_original_reader")
			defer readerDone()
			ref := roleTableRef(t, e, "public.network_inputs")
			input, _ := sessionPresentation(reader.session)
			raw := statement(t, "SELECT "+test.expression).(*parser.Select).Targets[0]
			e.mu.Lock()
			auth, err := e.authorizationLocked(reader)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			binder := newExpressionBinder(context.Background(), e, input, reader, nil)
			binder.queryAuth, binder.ref = &auth, ref
			binder.relation = auth.catalog.MustRelation(ref.Address)
			node, err := binder.bind(raw, expressionPolicy{context: projectionContext})
			e.mu.Unlock()
			if err != nil {
				t.Fatal("actual network operator selection", err)
			}
			outer, err := expressionRoutineDescriptor(node)
			if err != nil || outer == nil || outer.builtin == nil {
				t.Fatal("missing selected declaration", outer, err)
			}
			declaration := outer.builtin
			wantIdentity := routineIdentity{test.id, ids.PGCatalogNamespaceOID, test.name, procedureSignature(test.formals)}
			if declaration.identity != wantIdentity || declaration.result != test.result || declaration.kind != functionRoutine || !declaration.strict || declaration.volatility != Immutable || declaration.parallel != 's' || declaration.returnsSet || declaration.variadic != 0 || declaration.defaults != 0 || declaration.providers != operatorRoutineProvider || ids.OID(node.ResultUsage().Type.ID()) != test.result {
				t.Fatal("released canonical declaration", declaration, node.ResultUsage())
			}
			want := map[ids.OID]bool{test.id: true}
			if test.id == 2631 {
				// RETURN $2 + $1: the existing provider commutes the two inputs;
				// this real SQL occurrence selects inetpl(INET,INT8), not itself.
				want[2630] = true
				if len(outer.body) != 1 || outer.body[0].routine.address.ObjectID != 2630 || outer.body[0].demand != bodyAlways {
					t.Fatal("real reversed SQL body edge", outer.body)
				}
			} else if len(outer.body) != 0 {
				t.Fatal("INTERNAL operator acquired SQL helper edges", outer.body)
			}
			found := make(map[ids.OID]bool)
			for _, dependency := range binder.dependencies {
				if dependency.ref.Address.ClassID == ids.ProcedureClassID {
					found[dependency.ref.Address.ObjectID] = true
				}
			}
			if !reflect.DeepEqual(found, want) {
				t.Fatal("exact original routine manifest", found, want)
			}
			evaluation := EvalContext{Context: context.Background(), routineAuthority: &auth, routineRecipe: true, routineChecks: binder.dependencies}
			if err := admitRoutineCall(&evaluation, node, true, false); err != nil {
				t.Fatal("original authority", err)
			}
			foreign := New()
			for id := range want {
				for _, mode := range []string{"missing", "foreign", "stale"} {
					var checks []expressionDependency
					for _, dependency := range binder.dependencies {
						if dependency.ref.Address == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}) {
							switch mode {
							case "missing":
								continue
							case "foreign":
								dependency.ref = foreign.catalog.domain.Ref(dependency.ref.Epoch, dependency.ref.Address)
							case "stale":
								dependency.revision++
							}
						}
						checks = append(checks, dependency)
					}
					evaluation.routineChecks = checks
					if err := admitRoutineCall(&evaluation, node, true, false); err == nil {
						t.Fatal("accepted invalid original routine lifetime", id, mode)
					}
				}
			}
			remapped, _, err := retagBoundTypes(foreign.CatalogRead(), node, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				current, _ := foreign.CatalogRead().LookupType(old.ID())
				return current
			})
			if err != nil {
				t.Fatal(err)
			}
			evaluation.routineChecks = []expressionDependency{}
			if err := admitRoutineCall(&evaluation, remapped, true, false); err == nil {
				t.Fatal("foreign retag manufactured original lifetime")
			}
			evaluation.routineChecks = binder.dependencies
			if err := admitRoutineCall(&evaluation, node, true, false); err != nil {
				t.Fatal("negative probes changed source authority", err)
			}
		})
	}
}
