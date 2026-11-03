package functions_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// Transfer the actual codec-producing stage through an actual statement owner.
// The returned change belongs to the transaction journal, not a test substitute.
func visibilityCopyJournal(t *testing.T, e *core.Engine, actor *session.Session, ref ids.ObjectRef, old *catalog.Type) session.StorageChange {
	t.Helper()
	stage := visibilityCodecStage(t, e, ref, old)
	prepared, err := e.ValidateCatalogStage(t.Context(), stage)
	if err != nil {
		t.Fatal(err)
	}
	scope, finish := visibilityScope(t, e, actor)
	_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	if err = finish(err); err != nil {
		t.Fatal(err)
	}
	journal := actor.Transactions.Catalog
	if journal == nil {
		t.Fatal("missing real transaction journal")
	}
	change, ok := journal.Storage[ref.Address]
	if !ok || !change.PreservesVisibility || change.Original == nil || change.Replacement == change.Original || change.ObservedVisibility != change.Replacement.Visibility {
		t.Fatal("missing actual preserving copy/first VM observation", change)
	}
	return change
}

func TestPgvisibilityCheckPreparedPublication(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "stale_before_prepare"}[stale], func(t *testing.T) {
			e, ref, old, root := visibilityCodecFixture(t)
			original := e.Storage.Relations[ref.Address]
			base := e.CatalogRead().State()
			actor := enginetest.StartSession(t, e, nil)
			finisher := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, actor, "BEGIN")
			defer func() {
				if actor.TransactionState() != session.TxIdle {
					enginetest.SessionSQL(t, actor, "ROLLBACK")
				}
			}()
			change := visibilityCopyJournal(t, e, actor, ref, old)
			if change.Original != original || change.ObservedVisibility != root {
				t.Fatal("wrong first predecessor", change)
			}
			if stale {
				visibilityTruncateInSession(t, e, finisher, ref)
			}
			currentMap := original.Visibility
			if stale && (currentMap == root || !currentMap.Invalidated) {
				t.Fatal("real predecessor truncation did not publish")
			}
			// PREPARE transfers the journal even though its copied VM is now stale:
			// no physical heap/map is installed at this ownership boundary.
			enginetest.SessionSQL(t, actor, "PREPARE TRANSACTION 'visibility_codec_prepared'")
			e.Mu.Lock()
			entry := core.EnginePreparedRef(e).Lookup("visibility_codec_prepared")
			e.Mu.Unlock()
			if entry == nil || entry.Catalog == nil || actor.TransactionState() != session.TxIdle || actor.Transactions.Catalog != nil {
				t.Fatal("PREPARE did not transfer ownership")
			}
			transferred := entry.Catalog.Storage[ref.Address]
			if transferred.Original != original || transferred.Replacement != change.Replacement || !transferred.PreservesVisibility || transferred.ObservedVisibility != root || transferred.Replacement.Visibility != root {
				t.Fatal("PREPARE refreshed/lost first-copy authority", transferred)
			}
			if e.CatalogRead().State() != base || e.Storage.Relations[ref.Address] != original || original.Visibility != currentMap {
				t.Fatal("PREPARE installed a physical prefix")
			}
			defer func() {
				e.Mu.Lock()
				pending := core.EnginePreparedRef(e).Lookup("visibility_codec_prepared") != nil
				e.Mu.Unlock()
				if pending {
					enginetest.SessionSQL(t, finisher, "ROLLBACK PREPARED 'visibility_codec_prepared'")
				}
			}()
			actor.RequestEnd(session.CleanClose)
			if err := actor.WaitEnd(t.Context()); err != nil {
				t.Fatal(err)
			}
			err := visibilitySessionFailure(t, e, finisher, "COMMIT PREPARED 'visibility_codec_prepared'")
			e.Mu.Lock()
			remaining := core.EnginePreparedRef(e).Lookup("visibility_codec_prepared")
			e.Mu.Unlock()
			if stale {
				var conflict catalog.Conflict
				if !errors.As(err, &conflict) {
					t.Fatal("stale prepared materialization was not rejected", err)
				}
				if remaining != entry || e.CatalogRead().State() != base || e.CatalogRead().Get(old.Address()) != old || e.Storage.Relations[ref.Address] != original || original.Visibility != currentMap || !currentMap.Invalidated {
					t.Fatal("failed COMMIT PREPARED consumed ownership or installed a prefix")
				}
			} else {
				if err != nil || remaining != nil {
					t.Fatal("valid prepared copy did not materialize", err, remaining)
				}
				installed := e.Storage.Relations[ref.Address]
				if installed != change.Replacement || installed.Visibility != root || e.CatalogRead().Get(old.Address()) == old || installed.Versions[0].Values[0].Type().EnumMembers()[0].Label != "renamed" {
					t.Fatal("valid prepared publication lost real codec/map state")
				}
			}
			if root.Invalidated || root.Map.AllVisible != 1 || old.SQLType.EnumMembers()[0].Label != "old" {
				t.Fatal("immutable predecessor/candidate mutated")
			}
		})
	}
}
