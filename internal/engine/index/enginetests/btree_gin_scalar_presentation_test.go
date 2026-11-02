package enginetests_test

import (
	"context"
	"errors"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"testing"
)

func TestBtreeGINScalarPresentation(t *testing.T) {
	for _, populated := range []bool{false, true} {
		name := "empty"
		if populated {
			name = "populated"
		}
		t.Run(name, func(t *testing.T) {
			e, idx, original := scalarEngine(t, populated)
			view := e.CatalogRead()
			before, entries := ginCopyPayload(original.Gin), gistCopyEntries(original)
			enginetest.Run(t, e, "CREATE SCHEMA scalar_private_dest")
			enginetest.Run(t, e, "CREATE ROLE scalar_private_owner")
			enginetest.Run(t, e, "GRANT USAGE,CREATE ON SCHEMA public,scalar_private_dest TO scalar_private_owner")
			view = e.CatalogRead()
			enginetest.Run(t, e, "CREATE INDEX scalar_produced_i ON scalar_private USING gin((btint4cmp(int4,0)) int4_ops)")
			producedIndex, producedRoot := ginInstalledRoot(t, e, "scalar_produced_i")
			if !producedIndex.SQLIndex.RequiresInsertionOutput() {
				t.Fatal("callable key did not produce output")
			}
			view = e.CatalogRead()
			session := enginetest.StartSession(t, e, nil)
			tx := enginetest.TransactionBegin(t, session)
			enginetest.SessionSQL(t, session, scalarPrivateInsert(6, true))
			writes := session.Transactions.Writes[idx.SQLIndex.Parent]
			if len(writes) != 1 {
				t.Fatal("missing private row")
			}
			output, ok := writes[0].IndexOutputs[producedIndex.Address()]
			if !ok || !output.Member || !index.OutputMatches(output, producedIndex) {
				t.Fatal("missing produced keys")
			}
			enginetest.SessionSQL(t, session, "ALTER EXTENSION btree_gin SET SCHEMA scalar_private_dest")
			e.Mu.Lock()
			privateView := e.PrivateCatalogLocked(session.Transactions.Catalog)
			privateIndex := privateView.MustRelation(idx.Address())
			privateRoot := index.EffectiveRoot(e.Storage.IndexRoots, session.Transactions.Catalog, idx.Address())
			privateProduced := privateView.MustRelation(producedIndex.Address())
			privateProducedRoot := index.EffectiveRoot(e.Storage.IndexRoots, session.Transactions.Catalog, producedIndex.Address())
			e.Mu.Unlock()
			networkSharedPresentation(t, original, privateRoot)
			compoundSharedPresentation(t, producedRoot, privateProducedRoot)
			for col := range scalarPrivateCases {
				if privateIndex.SQLIndex.Keys[col].Class != privateRoot.Gin.Columns[col].Binding.Class {
					t.Fatal("private live class capture", col)
				}
			}
			nextWrites := session.Transactions.Writes[idx.SQLIndex.Parent]
			next, ok := nextWrites[0].IndexOutputs[producedIndex.Address()]
			if !ok || next.Index != privateProduced || !next.Member || !slices.Equal(output.Keys, next.Keys) || &output.Keys[0] != &next.Keys[0] || &writes[0].Values[0] != &nextWrites[0].Values[0] {
				t.Fatal("presentation reconstructed private typed outputs")
			}
			if e.Storage.IndexRoots[idx.Address()] != original || e.CatalogRead().State() != view.State() {
				t.Fatal("private presentation escaped")
			}
			enginetest.TransactionEnd(t, session, tx, false)
			if e.Storage.IndexRoots[idx.Address()] != original || e.Storage.IndexRoots[producedIndex.Address()] != producedRoot || e.CatalogRead().State() != view.State() {
				t.Fatal("presentation rollback changed predecessor")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := enginetest.Execute(e, ctx, enginetest.Statement(t, "ALTER EXTENSION btree_gin SET SCHEMA scalar_private_dest"), nil)
			if !errors.Is(err, context.Canceled) || e.Storage.IndexRoots[idx.Address()] != original || e.CatalogRead().State() != view.State() {
				t.Fatal("canceled presentation changed owner", err)
			}
			enginetest.Run(t, e, "ALTER EXTENSION btree_gin SET SCHEMA scalar_private_dest")
			_, relocated := ginInstalledRoot(t, e, "scalar_private_i")
			networkSharedPresentation(t, original, relocated)
			for col, tc := range scalarPrivateCases {
				old, current := original.Gin.Columns[col].Binding.Class, relocated.Gin.Columns[col].Binding.Class
				if old.OID() != current.OID() || old.FamilyOID() != current.FamilyOID() || old.Support(2).Routine != current.Support(2).Routine || old.Support(2).RoutineRevision == current.Support(2).RoutineRevision {
					t.Fatal("presentation changed identity or missed revision", tc.typ)
				}
				if _, err := routine.BindClassSupport(e.CatalogRead(), old); err == nil {
					t.Fatal("detached capture accepted")
				}
				for _, q := range []string{
					"ALTER OPERATOR CLASS scalar_private_dest." + tc.typ + "_ops USING gin RENAME TO scalar_current_" + tc.typ,
					"ALTER OPERATOR FAMILY scalar_private_dest." + tc.typ + "_ops USING gin OWNER TO scalar_private_owner",
					"ALTER FUNCTION scalar_private_dest.gin_extract_value_" + tc.typ + "(" + tc.typ + ",internal) OWNER TO scalar_private_owner",
				} {
					priorIndex, prior := ginInstalledRoot(t, e, "scalar_private_i")
					saved := ginCopyPayload(prior.Gin)
					enginetest.Run(t, e, q)
					currentIndex, currentRoot := ginInstalledRoot(t, e, "scalar_private_i")
					networkSharedPresentation(t, prior, currentRoot)
					if currentIndex == priorIndex || currentIndex.SQLIndex.Keys[col].Class != currentRoot.Gin.Columns[col].Binding.Class || !scalarSameGin(prior.Gin, saved) {
						t.Fatal("live presentation capture/backing", q)
					}
				}
			}
			// A fifth search revision is executable authority, not a presentation change.
			beforeIndex, beforeRoot := ginInstalledRoot(t, e, "scalar_private_i")
			beforeView := e.CatalogRead()
			enginetest.Run(t, e, "ALTER OPERATOR CLASS scalar_private_dest.scalar_current_float8 USING gin RENAME TO scalar_final_float8")
			afterIndex, afterRoot := ginInstalledRoot(t, e, "scalar_private_i")
			afterView := e.CatalogRead()
			class := afterIndex.SQLIndex.Keys[4].Class
			member, _ := afterView.FamilyOperator(ids.OID(class.FamilyOID()), 701, 701, 5, 's')
			state := catalog.CloneState(afterView.State())
			changed := *member
			changed.ObjectRevision = member.Revision() + 1
			state.SetDescriptor(changed.Address(), &changed)
			badView := afterView.WithState(state)
			metadata, _ := badView.IndexClass(ids.OID(class.OID()))
			fresh, err := routine.BindClassSupport(badView, metadata)
			if err != nil {
				t.Fatal("revision fixture", err)
			}
			candidate, definition := *afterIndex, *afterIndex.SQLIndex
			definition.Keys = slices.Clone(definition.Keys)
			definition.Keys[4].Class = fresh
			candidate.SQLIndex = &definition
			parent := beforeView.MustRelation(idx.SQLIndex.Parent)
			if out, err := index.RebindGinPresentationRoot(beforeView, badView, parent, parent, beforeIndex, &candidate, beforeRoot); err == nil || out != nil || e.Storage.IndexRoots[idx.Address()] != afterRoot {
				t.Fatal("fifth search authority laundered", out, err)
			}
			enginetest.Run(t, e, scalarPrivateInsert(6, true))
			_, written := ginInstalledRoot(t, e, "scalar_private_i")
			ginAssertOwners(t, written, e.Storage.Relations[idx.SQLIndex.Parent])
			if len(written.Entries) != len(original.Entries)+1 {
				t.Fatal("presentation lost indexed writes")
			}
			if !scalarSameGin(original.Gin, before) || !scalarSameEntries(original.Entries, entries) {
				t.Fatal("presentation mutated saved predecessor")
			}
		})
	}
}
