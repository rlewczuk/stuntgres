package tablecmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestSequenceDeleteCompositionPublicationFailure(t *testing.T) {
	for _, phase := range []string{"frontier", "predecessor", "shape", "namespace", "final_cancel"} {
		t.Run(phase, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(0),(1),(1),(2)")
			enginetest.SessionSQL(t, s, "DELETE FROM t WHERE v=0")
			enginetest.SessionSQL(t, s, "CREATE SEQUENCE s")
			sequenceScalar(t, s, "SELECT nextval('s')", 1)
			table := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "s")
			healthyFrontier, healthyNamespace := e.Visibility.CommitSeq(), e.Catalog.State.NamespaceGeneration()
			ref := enginetest.TransactionBegin(t, s)
			enginetest.SessionSQL(t, s, "ALTER SEQUENCE s RESTART 100")
			if r := enginetest.SessionSQL(t, s, "DELETE FROM t WHERE v=1"); r.Count != 2 {
				t.Fatal(r)
			}
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(3),(5)")
			enginetest.SessionSQL(t, s, "DELETE FROM t WHERE v=5")
			if phase == "namespace" {
				// No TEMP object has ever committed in this session: this is a
				// genuine new namespace descriptor, not another temp relation.
				enginetest.SessionSQL(t, s, "CREATE TEMP SEQUENCE first_temp START 40")
				state := catalog.CloneState(e.Catalog.State)
				state.SetNamespaceGeneration(^uint64(0))
				e.Catalog.PublishLocked(state)
			}
			switch phase {
			case "frontier":
				e.Visibility.SetCommitSeqLocked(ids.CommitSeq(^uint64(0)))
			case "predecessor":
				for id, candidate := range s.Transactions.Deletes[table.Address].Rows {
					candidate.Version++
					s.Transactions.Deletes[table.Address].Rows[id] = candidate
					break
				}
			case "shape":
				// Preserve every row and its width; only shape authority changes.
				replacement := *e.Storage.Relations[table.Address]
				e.Mu.Lock()
				e.Storage.InstallRelationLocked(table.Address, &replacement)
				e.Mu.Unlock()
			}
			assertSequenceDeletePrivateBatch(t, s, table.Address, sequence.Address, phase == "namespace")
			before := captureSequenceDeletePublication(e, s, table.Address)
			ticket := s.Transactions.Ticket
			if ticket == nil || len(e.Locks.Inspect(ticket).Journals[ticket]) == 0 {
				t.Fatal("mixed staging did not acquire transaction resources")
			}
			op := enginetest.TransactionOperation(t, s)
			var cancellation *cancelAtErrCheck
			if phase == "final_cancel" {
				base, cancel := context.WithCancel(op.Context())
				defer cancel()
				// Native Commit: admission Err(1), accepted-end Err(2),
				// preparePrivateCatalog Err(3), final publication Err(4).
				// The existing real-context seam cancels at that final check,
				// after row/end/catalog/generator candidates are prepared.
				cancellation = &cancelAtErrCheck{Context: base, cancel: cancel, at: 4}
				op.Ctx = cancellation
			}
			result, err := e.Commit(op, ref)
			op.Done()
			if result.Committed || err == nil {
				t.Fatal("failed mixed transaction committed", result, err)
			}
			switch phase {
			case "frontier":
				if err.Error() != "stuntgres: commit sequence exhausted" {
					t.Fatal(err)
				}
			case "predecessor":
				if err.Error() != "stuntgres: invalid deletion predecessor" {
					t.Fatal(err)
				}
			case "shape":
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
			case "namespace":
				if err.Error() != "stuntgres: namespace invalidation generation exhausted" {
					t.Fatal(err)
				}
			case "final_cancel":
				if !errors.Is(err, context.Canceled) || cancellation.checks.Load() != 4 {
					t.Fatal("did not reach final combined cancellation boundary", err, cancellation.checks.Load())
				}
			}
			before.assertUnchanged(t, e, s)
			assertSequenceDeleteEndCleanup(t, e, s, ticket)
			// Restore only deliberately exhausted counters, after checking
			// failure atomicity and disposal. Never restore private effects.
			if phase == "frontier" {
				e.Visibility.SetCommitSeqLocked(healthyFrontier)
			}
			if phase == "namespace" {
				state := catalog.CloneState(e.Catalog.State)
				state.SetNamespaceGeneration(healthyNamespace)
				e.Catalog.PublishLocked(state)
			}
			enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 1, 2)
			sequenceScalar(t, s, "SELECT currval('s')", 1)
			sequenceScalar(t, s, "SELECT lastval()", 1)
			peer := enginetest.StartSession(t, e, nil)
			sequenceScalar(t, peer, "SELECT nextval('s')", 2)
			// A fresh mixed transaction must reacquire the retired resources
			// and successfully publish through the same combined branch.
			next := enginetest.TransactionBegin(t, s)
			enginetest.SessionSQL(t, s, "ALTER SEQUENCE s RESTART 200")
			enginetest.SessionSQL(t, s, "DELETE FROM t WHERE v=2")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(9)")
			if phase == "namespace" {
				enginetest.SessionSQL(t, s, "CREATE TEMP SEQUENCE first_temp START 40")
			}
			frontier := e.Visibility.CommitSeq()
			if r := enginetest.TransactionEnd(t, s, next, true); !r.Committed || e.Visibility.CommitSeq() != frontier+1 {
				t.Fatal("healthy combined transaction failed", r, e.Visibility.CommitSeq(), frontier)
			}
			enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY v", 1, 1, 9)
			sequenceScalar(t, peer, "SELECT nextval('s')", 200)
			if phase == "namespace" {
				sequenceScalar(t, s, "SELECT nextval('first_temp')", 40)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestSequenceDeleteCompositionSingleFrontier(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(1),(2)")
	enginetest.SessionSQL(t, s, "CREATE SEQUENCE s")
	sequenceScalar(t, s, "SELECT nextval('s')", 1)
	table := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	frontier := e.Visibility.CommitSeq()
	ref := enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "ALTER SEQUENCE s RESTART 100")
	enginetest.SessionSQL(t, s, "DELETE FROM t WHERE v=1")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(3),(5)")
	enginetest.SessionSQL(t, s, "DELETE FROM t WHERE v=5")
	op := enginetest.TransactionOperation(t, s)
	if _, err := e.Commit(op, ref); err != nil {
		t.Fatal(err)
	}
	store := e.Storage.Relations[table.Address]
	if e.Visibility.CommitSeq() != frontier+1 || len(store.Versions) != 5 || store.Ends == nil || len(store.Ends.Rows) != 3 {
		t.Fatal("mixed publication did not install one frontier", e.Visibility.CommitSeq(), frontier, len(store.Versions), store.Ends)
	}
	for _, version := range store.Versions[3:] {
		if version.Born != frontier+1 {
			t.Fatal("append frontier", version.Born)
		}
	}
	for _, end := range store.Ends.Rows {
		if end.End != frontier+1 {
			t.Fatal("deletion frontier", end.End)
		}
	}
	peer := enginetest.StartSession(t, e, nil)
	enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY v", 2, 3)
	sequenceScalar(t, peer, "SELECT nextval('s')", 100)
}
