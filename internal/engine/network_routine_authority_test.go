package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
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
			if err != nil || outer == nil || outer.Builtin == nil {
				t.Fatal("missing selected declaration", outer, err)
			}
			declaration := outer.Builtin
			wantIdentity := routineIdentity{test.id, ids.PGCatalogNamespaceOID, test.name, procedureSignature(test.formals)}
			if declaration.Identity != wantIdentity || declaration.Result != test.result || declaration.Kind != functionRoutine || !declaration.Strict || declaration.Volatility != expr.Immutable || declaration.Parallel != 's' || declaration.ReturnsSet || declaration.Variadic != 0 || declaration.Defaults != 0 || declaration.Providers != operatorRoutineProvider || ids.OID(node.ResultUsage().Type.ID()) != test.result {
				t.Fatal("released canonical declaration", declaration, node.ResultUsage())
			}
			want := map[ids.OID]bool{test.id: true}
			if test.id == 2631 {
				// RETURN $2 + $1: the existing provider commutes the two inputs;
				// this real SQL occurrence selects inetpl(INET,INT8), not itself.
				want[2630] = true
				if len(outer.Body) != 1 || outer.Body[0].Routine.Address().ObjectID != 2630 || outer.Body[0].Demand != bodyAlways {
					t.Fatal("real reversed SQL body edge", outer.Body)
				}
			} else if len(outer.Body) != 0 {
				t.Fatal("INTERNAL operator acquired SQL helper edges", outer.Body)
			}
			found := make(map[ids.OID]bool)
			for _, dependency := range binder.dependencies {
				if dependency.Ref.Address.ClassID == ids.ProcedureClassID {
					found[dependency.Ref.Address.ObjectID] = true
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
					var checks []catalog.ExpressionDependency
					for _, dependency := range binder.dependencies {
						if dependency.Ref.Address == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}) {
							switch mode {
							case "missing":
								continue
							case "foreign":
								dependency.Ref = foreign.catalog.Domain.Ref(dependency.Ref.Epoch, dependency.Ref.Address)
							case "stale":
								dependency.Revision++
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
			evaluation.routineChecks = []catalog.ExpressionDependency{}
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
