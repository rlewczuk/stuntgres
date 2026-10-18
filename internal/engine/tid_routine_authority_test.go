package engine

import (
	"context"
	"testing"

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
			binder.relation = auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
			node, err := binder.bind(raw, expressionPolicy{context: projectionContext})
			e.mu.Unlock()
			if err != nil {
				t.Fatal("actual TID operator selection", err)
			}
			outer, err := expressionRoutineDescriptor(node)
			if err != nil || outer == nil || outer.builtin == nil {
				t.Fatal("missing selected declaration", outer, err)
			}
			declaration := outer.builtin
			wantIdentity := routineIdentity{test.id, PGCatalogNamespaceOID, test.name, "27,27"}
			if declaration.identity != wantIdentity || declaration.result != 16 || declaration.kind != functionRoutine || !declaration.strict || declaration.volatility != Immutable || declaration.parallel != 's' || declaration.returnsSet || declaration.variadic != 0 || declaration.defaults != 0 || declaration.providers != operatorRoutineProvider || node.ResultUsage().Type.ID() != sqltype.Bool {
				t.Fatal("released canonical declaration", declaration, node.ResultUsage())
			}
			address := ids.ObjectAddress{ClassID: ProcedureClassID, ObjectID: test.id}
			if auth.catalog.state.descriptors[address] != outer || outer.address != address || outer.namespace != (ids.ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}) || outer.name != test.name || outer.signature != "27,27" || outer.result != 16 || outer.routineKind != functionRoutine || !outer.strict || outer.volatility != Immutable {
				t.Fatal("selected original owned descriptor", outer)
			}
			if len(outer.body) != 0 {
				t.Fatal("INTERNAL TID operator acquired SQL helper edges", outer.body)
			}
			wantRef := e.domain.Ref(auth.catalog.Epoch(), address)
			procedures := 0
			for _, dependency := range binder.dependencies {
				if dependency.ref.Address.ClassID != ProcedureClassID {
					continue
				}
				procedures++
				if dependency.ref != wantRef || dependency.revision != outer.revision {
					t.Fatal("original selected reference and revision", dependency, wantRef, outer.revision)
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
					if dependency.ref.Address == address {
						switch mode {
						case "missing":
							continue
						case "foreign":
							dependency.ref = foreign.domain.Ref(dependency.ref.Epoch, dependency.ref.Address)
						case "stale epoch":
							dependency.ref.Epoch++
						case "stale revision":
							dependency.revision++
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
