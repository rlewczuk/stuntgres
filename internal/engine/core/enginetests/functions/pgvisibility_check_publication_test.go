package functions_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func visibilityCodecFixture(t *testing.T) (*core.Engine, ids.ObjectRef, *catalog.Type, *storage.VisibilityRoot) {
	t.Helper()
	e := engine.New()
	for _, q := range []string{"CREATE EXTENSION pg_visibility", "CREATE TYPE visibility_mood AS ENUM ('old','other')", "CREATE TABLE visibility_heap(n visibility_mood)", "INSERT INTO visibility_heap VALUES('old')", "VACUUM (FREEZE) visibility_heap"} {
		enginetest.Run(t, e, q)
	}
	view := e.CatalogRead()
	ref := enginetest.CatalogRef(t, e, view, "visibility_heap")
	address := view.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "visibility_mood"})
	old := view.MustType(address)
	scope, finish := visibilityScope(t, e, enginetest.StartSession(t, e, nil))
	defer finish(nil)
	read := visibilityCheckNode(t, e, "pg_visibility_map_summary")
	result, err := core.HostOf(scope).Contrib().Pgvisibility(read, uint32(ref.Address.ObjectID), 0)
	if err != nil || result.AllVisible != 1 || result.AllFrozen != 1 {
		t.Fatal("codec-copy seed", result, err)
	}
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
	return e, ref, old, e.Storage.Relations[ref.Address].Visibility
}

func visibilityCodecStage(t *testing.T, e *core.Engine, ref ids.ObjectRef, old *catalog.Type) *core.CatalogStage {
	t.Helper()
	members := old.SQLType.EnumMembers()
	members[0].Label = "renamed"
	typ, err := sqltype.EnumWithMembers(old.SQLType.ID(), old.Name(), old.SQLType.SQLName(), members)
	if err != nil {
		t.Fatal(err)
	}
	next := *old
	next.SQLType = typ
	next.ObjectRevision++
	stage := e.BeginCatalogStage(e.CatalogRead())
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = e.ReownStagedTypeGenerationsLocked(stage)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	shape, ok := stage.RowShapes[ref.Address]
	if !ok || !shape.PreservesVisibility || shape.Original != e.Storage.Relations[ref.Address] || shape.Replacement == shape.Original || shape.Replacement.Versions[0].Values[0].Type() != typ || shape.Original.Versions[0].Values[0].Type() != old.SQLType || shape.ObservedVisibility != shape.Original.Visibility || shape.Replacement.Visibility != shape.ObservedVisibility {
		t.Fatal("not an actual preserving codec-copy stage", shape)
	}
	return stage
}

func visibilityTruncateInSession(t *testing.T, e *core.Engine, s *session.Session, ref ids.ObjectRef) {
	t.Helper()
	scope, finish := visibilityScope(t, e, s)
	defer finish(nil)
	n := visibilityCheckNode(t, e, "pg_truncate_visibility_map")
	if err := core.HostOf(scope).Contrib().PgvisibilityTruncate(n, uint32(ref.Address.ObjectID)); err != nil {
		t.Fatal(err)
	}
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
}

func visibilitySessionFailure(t *testing.T, e *core.Engine, s *session.Session, query string) error {
	t.Helper()
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(enginetest.Statement(t, query), 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	return scope.Finish(err)
}

func visibilityCheckCodecPublication(t *testing.T) {
	t.Run("staged_copy_then_real_truncate", func(t *testing.T) {
		e, ref, old, root := visibilityCodecFixture(t)
		original := e.Storage.Relations[ref.Address]
		stage := visibilityCodecStage(t, e, ref, old)
		prepared, err := e.ValidateCatalogStage(t.Context(), stage)
		if err != nil {
			t.Fatal(err)
		}
		strict := session.NewStorageReadFence(ref.Address, original)
		identity := session.NewStorageIdentityFence(ref.Address, original)
		visibilityTruncateInSession(t, e, enginetest.StartSession(t, e, nil), ref)
		if !strict.Matches(original) || !identity.Matches(original) {
			t.Fatal("VM mutation changed generic tuple read-fence semantics")
		}
		if stage.RowShapes[ref.Address].MatchesWithEnds(original) {
			t.Fatal("preserving candidate ignored changed predecessor VM")
		}
		_, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
		var conflict catalog.Conflict
		if !errors.As(err, &conflict) {
			t.Fatal("stale preserving copy publication accepted", err)
		}
		if e.Storage.Relations[ref.Address] != original || !original.Visibility.Invalidated || e.CatalogRead().Get(old.Address()) != old || stage.RowShapes[ref.Address].Replacement.Visibility != root || root.Invalidated || root.Map.AllVisible != 1 {
			t.Fatal("failed publication lost physical invalidation/old immutable roots")
		}
	})
	t.Run("metadata_same_store_truncate_commit", func(t *testing.T) {
		e, ref, _, root := visibilityCodecFixture(t)
		original := e.Storage.Relations[ref.Address]
		actor := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, actor, "BEGIN")
		defer func() {
			if actor.TransactionState() != session.TxIdle {
				enginetest.SessionSQL(t, actor, "ROLLBACK")
			}
		}()
		enginetest.SessionSQL(t, actor, "ALTER TABLE visibility_heap ALTER COLUMN n SET DEFAULT 'old'")
		if change, ok := actor.Transactions.Catalog.Storage[ref.Address]; ok && (change.Original != original || change.Replacement != original || change.PreservesVisibility) {
			t.Fatal("metadata-only DDL unexpectedly copied the heap", change)
		}
		visibilityTruncateInSession(t, e, actor, ref)
		empty := original.Visibility
		if empty == nil || !empty.Invalidated {
			t.Fatal("same-store truncate did not publish physical empty root")
		}
		if err := visibilitySessionFailure(t, e, actor, "COMMIT"); err != nil {
			t.Fatal("metadata-only same-store truncate rejected", err)
		}
		current := e.CatalogRead().MustRelation(ref.Address)
		if e.Storage.Relations[ref.Address] != original || original.Visibility != empty || len(current.ColumnMeta.Defaults) == 0 || root.Invalidated || root.Map.AllVisible != 1 {
			t.Fatal("metadata commit restored VM, lost metadata, or changed immutable history")
		}
	})
	t.Run("metadata_truncate_then_first_codec_copy", func(t *testing.T) {
		e, ref, _, root := visibilityCodecFixture(t)
		original := e.Storage.Relations[ref.Address]
		actor := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, actor, "BEGIN")
		defer func() {
			if actor.TransactionState() != session.TxIdle {
				enginetest.SessionSQL(t, actor, "ROLLBACK")
			}
		}()
		enginetest.SessionSQL(t, actor, "ALTER TABLE visibility_heap ALTER COLUMN n SET DEFAULT 'old'")
		visibilityTruncateInSession(t, e, actor, ref)
		empty := original.Visibility
		if empty == nil || !empty.Invalidated {
			t.Fatal("metadata predecessor was not physically truncated")
		}
		enginetest.SessionSQL(t, actor, "ALTER TYPE visibility_mood RENAME VALUE 'old' TO 'new'")
		change := actor.Transactions.Catalog.Storage[ref.Address]
		if change.Original != original || change.Replacement == original || !change.PreservesVisibility || change.ObservedVisibility != empty || change.Replacement.Visibility != empty {
			t.Fatal("first actual copy used an earlier metadata VM observation", change)
		}
		if err := visibilitySessionFailure(t, e, actor, "COMMIT"); err != nil {
			t.Fatal("first copy of current physical VM was spuriously rejected", err)
		}
		if e.Storage.Relations[ref.Address] != change.Replacement || change.Replacement.Visibility != empty || original.Visibility != empty || root.Invalidated || root.Map.AllVisible != 1 {
			t.Fatal("first preserving publication restored or changed physical VM")
		}
	})
	t.Run("destructive_then_codec_copy_stays_fresh", func(t *testing.T) {
		e, ref, _, root := visibilityCodecFixture(t)
		original := e.Storage.Relations[ref.Address]
		// Real ALTER TABLE owns an AX lock until COMMIT: mutate its predecessor
		// before that lock, never block a peer truncate behind our own transaction.
		visibilityTruncateInSession(t, e, enginetest.StartSession(t, e, nil), ref)
		empty := original.Visibility
		actor := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, actor, "BEGIN")
		defer func() {
			if actor.TransactionState() != session.TxIdle {
				enginetest.SessionSQL(t, actor, "ROLLBACK")
			}
		}()
		enginetest.SessionSQL(t, actor, "ALTER TABLE visibility_heap ADD COLUMN extra integer DEFAULT 0")
		first := actor.Transactions.Catalog.Storage[ref.Address]
		if first.Replacement == original || first.PreservesVisibility || first.Replacement.Visibility != nil {
			t.Fatal("destructive projection retained predecessor VM authority", first)
		}
		enginetest.SessionSQL(t, actor, "ALTER TYPE visibility_mood RENAME VALUE 'old' TO 'new'")
		change := actor.Transactions.Catalog.Storage[ref.Address]
		if change.Replacement == first.Replacement || change.PreservesVisibility || change.ObservedVisibility != empty || change.Replacement.Visibility != nil {
			t.Fatal("later codec transport reminted predecessor continuity", change)
		}
		if err := visibilitySessionFailure(t, e, actor, "COMMIT"); err != nil {
			t.Fatal("fresh replacement spuriously fenced predecessor VM", err)
		}
		if e.Storage.Relations[ref.Address] != change.Replacement || change.Replacement.Visibility != nil || original.Visibility != empty || !empty.Invalidated || root.Invalidated {
			t.Fatal("fresh modeled replacement inherited or restored predecessor VM")
		}
	})
	for _, phase := range []string{"commit_conflict", "repeated_copy_conflict", "savepoint_keeps_copy", "savepoint_discards_copy", "private_copy_truncate"} {
		t.Run(phase, func(t *testing.T) {
			e, ref, old, root := visibilityCodecFixture(t)
			original := e.Storage.Relations[ref.Address]
			actor := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, actor, "BEGIN")
			defer func() {
				if actor.TransactionState() != session.TxIdle {
					enginetest.SessionSQL(t, actor, "ROLLBACK")
				}
			}()
			enginetest.SessionSQL(t, actor, "SAVEPOINT before_copy")
			stage := visibilityCodecStage(t, e, ref, old)
			prepared, err := e.ValidateCatalogStage(t.Context(), stage)
			if err != nil {
				t.Fatal(err)
			}
			scope, finish := visibilityScope(t, e, actor)
			_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
			err = finish(err)
			if err != nil {
				t.Fatal(err)
			}
			journal := actor.Transactions.Catalog
			if journal == nil {
				t.Fatal("codec stage did not transfer to actual transaction journal")
			}
			change := journal.Storage[ref.Address]
			if change.Original != original || !change.PreservesVisibility || change.ObservedVisibility != root || change.Replacement == original {
				t.Fatal("journal lost first VM observation", change)
			}
			enginetest.SessionSQL(t, actor, "SAVEPOINT after_copy")
			if phase == "private_copy_truncate" {
				visibilityTruncateInSession(t, e, actor, ref)
				if original.Visibility != root || !change.Replacement.Visibility.Invalidated {
					t.Fatal("private target truncated wrong incarnation")
				}
			} else {
				visibilityTruncateInSession(t, e, enginetest.StartSession(t, e, nil), ref)
				if !original.Visibility.Invalidated {
					t.Fatal("peer real truncate did not mutate committed map")
				}
			}
			if phase == "repeated_copy_conflict" || phase == "private_copy_truncate" {
				enginetest.SessionSQL(t, actor, "ALTER TYPE visibility_mood RENAME VALUE 'renamed' TO 'again'")
				later := actor.Transactions.Catalog.Storage[ref.Address]
				if later.Original != original || !later.PreservesVisibility || later.ObservedVisibility != root || later.Replacement == change.Replacement {
					t.Fatal("later genuine codec copy refreshed first observation or lost continuity", later)
				}
				if phase == "private_copy_truncate" && !later.Replacement.Visibility.Invalidated {
					t.Fatal("later codec copy lost private physical truncation")
				}
				change = later
			}
			if phase == "savepoint_keeps_copy" || phase == "savepoint_discards_copy" {
				if err := visibilitySessionFailure(t, e, actor, "SELECT 1/0"); err == nil {
					t.Fatal("statement failure fixture succeeded")
				}
				point := "after_copy"
				if phase == "savepoint_discards_copy" {
					point = "before_copy"
				}
				enginetest.SessionSQL(t, actor, "ROLLBACK TO SAVEPOINT "+point)
				var kept session.StorageChange
				exists := false
				if actor.Transactions.Catalog != nil {
					kept, exists = actor.Transactions.Catalog.Storage[ref.Address]
				}
				if phase == "savepoint_keeps_copy" && (!exists || kept.ObservedVisibility != root || kept.Replacement != change.Replacement) {
					t.Fatal("savepoint refreshed first predecessor observation", kept)
				}
				if phase == "savepoint_discards_copy" && exists {
					t.Fatal("rolled-back statement/savepoint retained a codec copy")
				}
			}
			err = visibilitySessionFailure(t, e, actor, "COMMIT")
			if phase == "commit_conflict" || phase == "repeated_copy_conflict" || phase == "savepoint_keeps_copy" {
				var conflict catalog.Conflict
				if !errors.As(err, &conflict) {
					t.Fatal("journal committed stale copied VM", err)
				}
				if e.Storage.Relations[ref.Address] != original || !original.Visibility.Invalidated || e.CatalogRead().Get(old.Address()) != old {
					t.Fatal("failed commit published stale copy")
				}
			} else {
				if err != nil {
					t.Fatal("valid predecessor/private map publication rejected", err)
				}
				current := e.Storage.Relations[ref.Address]
				if current.Visibility == nil || !current.Visibility.Invalidated {
					t.Fatal("successful commit erased real physical truncation")
				}
				if phase == "private_copy_truncate" && (current != change.Replacement || original.Visibility != root) {
					t.Fatal("publication overwrote private VM with predecessor state")
				}
				if phase == "savepoint_discards_copy" && (current != original || e.CatalogRead().Get(old.Address()) != old) {
					t.Fatal("savepoint rolled-back type copy leaked")
				}
			}
			if root.Invalidated || root.Map.AllVisible != 1 {
				t.Fatal("historical map root mutated")
			}
		})
	}
}
