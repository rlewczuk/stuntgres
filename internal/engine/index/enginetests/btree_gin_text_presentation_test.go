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

func TestBtreeGINTextPresentation(t *testing.T) {
	for _, populated := range []bool{false, true} {
		name := "empty"
		if populated {
			name = "populated"
		}
		t.Run(name, func(t *testing.T) {
			e, idx, original := textEngine(t, populated)
			before, entries := ginCopyPayload(original.Gin), gistCopyEntries(original)
			enginetest.Run(t, e, "CREATE SCHEMA text_private_dest")
			enginetest.Run(t, e, "CREATE ROLE text_private_owner")
			enginetest.Run(t, e, "GRANT USAGE,CREATE ON SCHEMA public,text_private_dest TO text_private_owner")
			// Separate roots distinguish the two consumers of the same routine.
			enginetest.Run(t, e, "CREATE INDEX text_only_i ON text_private USING gin(c0 text_ops)")
			enginetest.Run(t, e, "CREATE INDEX varchar_only_i ON text_private USING gin(c1 varchar_ops)")
			_, textOld := ginInstalledRoot(t, e, "text_only_i")
			_, varcharOld := ginInstalledRoot(t, e, "varchar_only_i")
			view := e.CatalogRead()
			session := enginetest.StartSession(t, e, nil)
			tx := enginetest.TransactionBegin(t, session)
			enginetest.SessionSQL(t, session, "ALTER FUNCTION public.gin_extract_value_text(text,internal) RENAME TO text_extract_current")
			e.Mu.Lock()
			privateView := e.PrivateCatalogLocked(session.Transactions.Catalog)
			privateIndex := privateView.MustRelation(idx.Address())
			privateRoot := index.EffectiveRoot(e.Storage.IndexRoots, session.Transactions.Catalog, idx.Address())
			e.Mu.Unlock()
			networkSharedPresentation(t, original, privateRoot)
			for _, col := range []int{0, 1} {
				if privateIndex.SQLIndex.Keys[col].Class != privateRoot.Gin.Columns[col].Binding.Class || privateRoot.Gin.Columns[col].Binding.Class.Support(2).RoutineRevision == original.Gin.Columns[col].Binding.Class.Support(2).RoutineRevision {
					t.Fatal("shared private helper failed to refresh both classes", col)
				}
			}
			if e.Storage.IndexRoots[idx.Address()] != original || e.CatalogRead().State() != view.State() {
				t.Fatal("private presentation escaped")
			}
			enginetest.TransactionEnd(t, session, tx, false)
			if e.Storage.IndexRoots[idx.Address()] != original || e.CatalogRead().State() != view.State() {
				t.Fatal("rollback changed capture")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := enginetest.Execute(e, ctx, enginetest.Statement(t, "ALTER EXTENSION btree_gin SET SCHEMA text_private_dest"), nil); !errors.Is(err, context.Canceled) || e.Storage.IndexRoots[idx.Address()] != original {
				t.Fatal("canceled presentation published", err)
			}
			for _, q := range []string{
				"ALTER FUNCTION public.gin_extract_value_text(text,internal) RENAME TO text_extract_current",
				"ALTER FUNCTION public.text_extract_current(text,internal) OWNER TO text_private_owner",
				"ALTER FUNCTION public.text_extract_current(text,internal) SET SCHEMA text_private_dest",
				"ALTER EXTENSION btree_gin DROP FUNCTION text_private_dest.text_extract_current(text,internal)",
				"ALTER EXTENSION btree_gin ADD FUNCTION text_private_dest.text_extract_current(text,internal)",
			} {
				priorIndex, prior := ginInstalledRoot(t, e, "text_private_i")
				enginetest.Run(t, e, q)
				currentIndex, current := ginInstalledRoot(t, e, "text_private_i")
				networkSharedPresentation(t, prior, current)
				for _, col := range []int{0, 1} {
					if currentIndex.SQLIndex.Keys[col].Class != current.Gin.Columns[col].Binding.Class {
						t.Fatal("live class/root capture", col)
					}
					if _, err := routine.BindClassSupport(e.CatalogRead(), priorIndex.SQLIndex.Keys[col].Class); q != "ALTER EXTENSION btree_gin DROP FUNCTION text_private_dest.text_extract_current(text,internal)" && q != "ALTER EXTENSION btree_gin ADD FUNCTION text_private_dest.text_extract_current(text,internal)" && err == nil {
						t.Fatal("ordinary detached stale capture admitted", col)
					}
				}
				_, textCurrent := ginInstalledRoot(t, e, "text_only_i")
				_, varcharCurrent := ginInstalledRoot(t, e, "varchar_only_i")
				compoundSharedPresentation(t, textOld, textCurrent)
				compoundSharedPresentation(t, varcharOld, varcharCurrent)
				enginetest.Run(t, e, textPrivateInsert(90, true))
				_, written := ginInstalledRoot(t, e, "text_private_i")
				ginAssertOwners(t, written, e.Storage.Relations[idx.SQLIndex.Parent])
				enginetest.Run(t, e, "DELETE FROM text_private WHERE id=90")
				_, textOld = ginInstalledRoot(t, e, "text_only_i")
				_, varcharOld = ginInstalledRoot(t, e, "varchar_only_i")
			}
			beforeIndex, beforeRoot := ginInstalledRoot(t, e, "text_private_i")
			beforeView := e.CatalogRead()
			enginetest.Run(t, e, "ALTER OPERATOR CLASS public.varchar_ops USING gin RENAME TO text_varchar_current")
			afterIndex, afterRoot := ginInstalledRoot(t, e, "text_private_i")
			afterView := e.CatalogRead()
			class := afterIndex.SQLIndex.Keys[1].Class
			member, _ := afterView.FamilyOperator(ids.OID(class.FamilyOID()), 25, 25, 5, 's')
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
			definition.Keys[1].Class = fresh
			candidate.SQLIndex = &definition
			parent := beforeView.MustRelation(idx.SQLIndex.Parent)
			if out, err := index.RebindGinPresentationRoot(beforeView, badView, parent, parent, beforeIndex, &candidate, beforeRoot); err == nil || out != nil || e.Storage.IndexRoots[idx.Address()] != afterRoot {
				t.Fatal("fifth search authority laundered", out, err)
			}
			if !scalarSameGin(original.Gin, before) || !scalarSameEntries(original.Entries, entries) {
				t.Fatal("presentation mutated retained typed sources")
			}
		})
	}
}
