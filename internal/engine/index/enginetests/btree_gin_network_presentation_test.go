package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
)

func networkSharedPresentation(t *testing.T, before, after *storage.IndexRoot) {
	t.Helper()
	compoundSharedPresentation(t, before, after)
	for col := range networkPrivateCases {
		for slot := 1; slot <= 5; slot++ {
			if before.Gin.Columns[col].Binding.Class.Support(slot).Authority != after.Gin.Columns[col].Binding.Class.Support(slot).Authority {
				t.Fatal("presentation reminted genuine installation", col, slot)
			}
		}
	}
}
func TestBtreeGINNetworkPresentation(t *testing.T) {
	for _, populated := range []bool{false, true} {
		name := "empty"
		if populated {
			name = "populated"
		}
		t.Run(name, func(t *testing.T) {
			e, idx, original := networkEngine(t, populated)
			enginetest.Run(t, e, "CREATE SCHEMA network_private_dest")
			enginetest.Run(t, e, "CREATE ROLE network_private_owner")
			enginetest.Run(t, e, "GRANT USAGE,CREATE ON SCHEMA public,network_private_dest TO network_private_owner")
			// Both plain and callable keys retain produced placement. A callable
			// key additionally witnesses transport of its retained expression output.
			enginetest.Run(t, e, "CREATE INDEX network_private_produced_i ON network_private USING gin((macaddr8_cmp(macaddr8,'80:00:00:00:00:00:00:00'::macaddr8)) oid_ops)")
			producedIndex, producedRoot := ginInstalledRoot(t, e, "network_private_produced_i")
			if !producedIndex.SQLIndex.RequiresInsertionOutput() {
				t.Fatal("callable fixture does not require genuine insertion output")
			}
			view := e.CatalogRead()
			before, entries := ginCopyPayload(original.Gin), gistCopyEntries(original)
			session := enginetest.StartSession(t, e, nil)
			tx := enginetest.TransactionBegin(t, session)
			enginetest.SessionSQL(t, session, networkPrivateInsert(6, true))
			writes := session.Transactions.Writes[idx.SQLIndex.Parent]
			if len(writes) != 1 {
				t.Fatal("private fixture lacks its inserted row")
			}
			output, ok := writes[0].IndexOutputs[producedIndex.Address()]
			if !ok || !output.Member || !index.OutputMatches(output, producedIndex) || len(output.Keys) != 1 {
				t.Fatal("private fixture lacks actual callable produced output")
			}
			enginetest.SessionSQL(t, session, "ALTER EXTENSION btree_gin SET SCHEMA network_private_dest")
			e.Mu.Lock()
			privateView := e.PrivateCatalogLocked(session.Transactions.Catalog)
			privateIndex := privateView.MustRelation(idx.Address())
			privateRoot := index.EffectiveRoot(e.Storage.IndexRoots, session.Transactions.Catalog, idx.Address())
			privateProducedIndex := privateView.MustRelation(producedIndex.Address())
			privateProducedRoot := index.EffectiveRoot(e.Storage.IndexRoots, session.Transactions.Catalog, producedIndex.Address())
			e.Mu.Unlock()
			networkSharedPresentation(t, original, privateRoot)
			compoundSharedPresentation(t, producedRoot, privateProducedRoot)
			for col := range networkPrivateCases {
				selected := ginClass(t, privateView, ids.OID(privateIndex.SQLIndex.Keys[col].Class.OID()))
				if privateIndex.SQLIndex.Keys[col].Class != selected || privateRoot.Gin.Columns[col].Binding.Class != selected {
					t.Fatal("private live cohort stamps", col)
				}
			}
			nextWrites := session.Transactions.Writes[idx.SQLIndex.Parent]
			if len(writes) != len(nextWrites) || &writes[0].Values[0] != &nextWrites[0].Values[0] {
				t.Fatal("relocation reconstructed private values")
			}
			next, ok := nextWrites[0].IndexOutputs[producedIndex.Address()]
			if !ok || next.Index != privateProducedIndex || !next.Member || !slices.Equal(output.Keys, next.Keys) || len(next.Keys) != 1 || &output.Keys[0] != &next.Keys[0] {
				t.Fatal("relocation reconstructed private produced keys")
			}
			if e.Storage.IndexRoots[idx.Address()] != original || e.Storage.IndexRoots[producedIndex.Address()] != producedRoot || e.CatalogRead().State() != view.State() {
				t.Fatal("private cohort escaped")
			}
			enginetest.TransactionEnd(t, session, tx, false)
			if e.Storage.IndexRoots[idx.Address()] != original || e.Storage.IndexRoots[producedIndex.Address()] != producedRoot || e.CatalogRead().State() != view.State() {
				t.Fatal("rollback changed predecessor")
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := enginetest.Execute(e, canceled, enginetest.Statement(t, "ALTER EXTENSION btree_gin SET SCHEMA network_private_dest"), nil)
			if !errors.Is(err, context.Canceled) || e.Storage.IndexRoots[idx.Address()] != original || e.Storage.IndexRoots[producedIndex.Address()] != producedRoot || e.CatalogRead().State() != view.State() {
				t.Fatal("canceled cohort changed predecessor", err)
			}
			enginetest.Run(t, e, "ALTER EXTENSION btree_gin SET SCHEMA network_private_dest")
			_, relocated := ginInstalledRoot(t, e, "network_private_i")
			networkSharedPresentation(t, original, relocated)
			for col, tc := range networkPrivateCases {
				old := original.Gin.Columns[col].Binding.Class
				current := relocated.Gin.Columns[col].Binding.Class
				if old.Support(2).RoutineRevision == current.Support(2).RoutineRevision {
					t.Fatal("relocation omitted helper revision", tc.typ)
				}
				if _, err := routine.BindClassSupport(e.CatalogRead(), old); err == nil {
					t.Fatal("detached capture laundered", tc.typ)
				}
				for _, q := range []string{
					"ALTER OPERATOR CLASS network_private_dest." + tc.typ + "_ops USING gin RENAME TO network_current_" + tc.typ,
					"ALTER OPERATOR FAMILY network_private_dest." + tc.typ + "_ops USING gin OWNER TO network_private_owner",
					"ALTER FUNCTION network_private_dest.gin_extract_value_" + tc.typ + "(" + tc.typ + ",internal) OWNER TO network_private_owner",
				} {
					priorIndex, prior := ginInstalledRoot(t, e, "network_private_i")
					snapshot := ginCopyPayload(prior.Gin)
					enginetest.Run(t, e, q)
					currentIndex, current := ginInstalledRoot(t, e, "network_private_i")
					networkSharedPresentation(t, prior, current)
					if currentIndex == priorIndex || currentIndex.SQLIndex.Keys[col].Class != current.Gin.Columns[col].Binding.Class {
						t.Fatal("presentation omitted live key stamp", q)
					}
					if !reflect.DeepEqual(prior.Gin, snapshot) {
						t.Fatal("presentation mutated retained predecessor")
					}
				}
			}
			enginetest.Run(t, e, networkPrivateInsert(6, true))
			_, written := ginInstalledRoot(t, e, "network_private_i")
			if len(written.Entries) != len(original.Entries)+1 {
				t.Fatal("presentation lost live posting maintenance")
			}
			enginetest.Run(t, e, "DELETE FROM network_private WHERE id=6")
			if populated {
				beforeIndex, beforeRoot := ginInstalledRoot(t, e, "network_private_i")
				beforeView := e.CatalogRead()
				enginetest.Run(t, e, "ALTER OPERATOR CLASS network_private_dest.network_current_inet USING gin RENAME TO network_final_inet")
				afterIndex, afterRoot := ginInstalledRoot(t, e, "network_private_i")
				afterView := e.CatalogRead()
				class := afterIndex.SQLIndex.Keys[0].Class
				member, _ := afterView.FamilyOperator(ids.OID(class.FamilyOID()), 869, 869, 5, 's')
				operator, _ := afterView.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: member.Operator})
				procedure := afterView.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: operator.Function})
				for _, kind := range []string{"member", "operator", "routine", "provider", "owner"} {
					state := catalog.CloneState(afterView.State())
					switch kind {
					case "member":
						changed := *member
						changed.ObjectRevision = member.Revision() + 1
						state.SetDescriptor(changed.Address(), &changed)
					case "operator":
						changed := *operator
						changed.ObjectRevision = operator.Revision() + 1
						state.SetDescriptor(changed.Address(), &changed)
					case "routine":
						changed := *procedure
						changed.ObjectRevision = procedure.Revision() + 1
						state.SetDescriptor(changed.Address(), &changed)
					case "owner":
						changed := *member
						changed.InternalOwner = ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(class.OID())}
						changed.ObjectRevision = member.Revision() + 1
						state.SetDescriptor(changed.Address(), &changed)
					case "provider":
						helper := afterView.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(class.Support(5).Routine)})
						changed := *helper
						changed.Implementation = nil
						changed.ObjectRevision = helper.Revision() + 1
						state.SetDescriptor(changed.Address(), &changed)
					}
					badView := afterView.WithState(state)
					metadata, _ := badView.IndexClass(ids.OID(class.OID()))
					fresh, err := routine.BindClassSupport(badView, metadata)
					if kind == "provider" || kind == "owner" {
						if err == nil {
							t.Fatal("unsealed provider or wrong dependency owner accepted", kind)
						}
						continue
					}
					if err != nil {
						t.Fatal("revision fixture did not reach presentation certificate", kind, err)
					}
					candidate, definition := *afterIndex, *afterIndex.SQLIndex
					definition.Keys = slices.Clone(definition.Keys)
					definition.Keys[0].Class = fresh
					candidate.SQLIndex = &definition
					parent := beforeView.MustRelation(idx.SQLIndex.Parent)
					moved, err := index.RebindGinPresentationRoot(beforeView, badView, parent, parent, beforeIndex, &candidate, beforeRoot)
					if err == nil || moved != nil || e.Storage.IndexRoots[idx.Address()] != afterRoot {
						t.Fatal("changed fifth authority used presentation transport", kind, moved, err)
					}
				}
			}
			if !reflect.DeepEqual(original.Gin, before) || !reflect.DeepEqual(original.Entries, entries) {
				t.Fatal("presentation mutated original value graph")
			}
		})
	}
}
