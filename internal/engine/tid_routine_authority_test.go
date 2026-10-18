package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The expected identities are independently enumerated REL_18_0 declarations.
// Real row operands preserve the selected operator rather than a folded constant.
func TestTIDRoutineAuthority(t *testing.T) {
	for _, test := range []struct {
		operator, name string
		id             ids.OID
	}{
		{"=", "tideq", 1292},
		{"<>", "tidne", 1265},
		{"<", "tidlt", 2791},
		{"<=", "tidle", 2793},
		{">=", "tidge", 2792},
		{">", "tidgt", 2790},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := New()
			admin, finish := roleOperation(t, e, "postgres")
			roleExecute(t, e, admin, "CREATE ROLE tid_original_reader LOGIN")
			roleExecute(t, e, admin, "CREATE TABLE tid_inputs(lhs tid,rhs tid)")
			finish()
			// Admit the owned reader statement only after its input DDL exists.
			reader, readerDone := roleOperation(t, e, "tid_original_reader")
			defer readerDone()
			ref := roleTableRef(t, e, "public.tid_inputs")
			input, _ := sessionPresentation(reader.session)
			raw := statement(t, "SELECT lhs "+test.operator+" rhs").(*parser.Select).Targets[0]
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
				t.Fatal("actual TID operator selection", err)
			}
			outer, err := expressionRoutineDescriptor(node)
			if err != nil || outer == nil || outer.Builtin == nil {
				t.Fatal("missing selected declaration", outer, err)
			}
			declaration := outer.Builtin
			wantIdentity := routineIdentity{test.id, ids.PGCatalogNamespaceOID, test.name, "27,27"}
			if declaration.Identity != wantIdentity || declaration.Result != 16 || declaration.Kind != functionRoutine || !declaration.Strict || declaration.Volatility != expr.Immutable || declaration.Parallel != 's' || declaration.ReturnsSet || declaration.Variadic != 0 || declaration.Defaults != 0 || declaration.Providers != operatorRoutineProvider || node.ResultUsage().Type.ID() != sqltype.Bool {
				t.Fatal("released canonical declaration", declaration, node.ResultUsage())
			}
			address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: test.id}
			if auth.catalog.Get(address) != outer || outer.Address() != address || outer.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || outer.Name() != test.name || outer.Signature != "27,27" || outer.Result != 16 || outer.RoutineKind != functionRoutine || !outer.Strict || outer.Volatility != expr.Immutable {
				t.Fatal("selected original owned descriptor", outer)
			}
			if len(outer.Body) != 0 {
				t.Fatal("INTERNAL TID operator acquired SQL helper edges", outer.Body)
			}
			wantRef := e.catalog.Domain.Ref(auth.catalog.Epoch(), address)
			procedures := 0
			for _, dependency := range binder.dependencies {
				if dependency.Ref.Address.ClassID != ids.ProcedureClassID {
					continue
				}
				procedures++
				if dependency.Ref != wantRef || dependency.Revision != outer.Revision() {
					t.Fatal("original selected reference and revision", dependency, wantRef, outer.Revision())
				}
			}
			if procedures != 1 {
				t.Fatal("exact original routine manifest", binder.dependencies)
			}
			evaluation := EvalContext{Context: context.Background(), routineAuthority: &auth, routineRecipe: true, routineChecks: binder.dependencies}
			if err := admitRoutineCall(&evaluation, node, true, false); err != nil {
				t.Fatal("original authority", err)
			}
			foreign := New()
			for _, mode := range []string{"missing", "foreign", "stale epoch", "stale revision"} {
				var checks []expressionDependency
				for _, dependency := range binder.dependencies {
					if dependency.Ref.Address == address {
						switch mode {
						case "missing":
							continue
						case "foreign":
							dependency.Ref = foreign.catalog.Domain.Ref(dependency.Ref.Epoch, dependency.Ref.Address)
						case "stale epoch":
							dependency.Ref.Epoch++
						case "stale revision":
							dependency.Revision++
						}
					}
					checks = append(checks, dependency)
				}
				evaluation.routineChecks = checks
				if err := admitRoutineCall(&evaluation, node, true, false); err == nil {
					t.Fatal("accepted invalid original routine lifetime", mode)
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
