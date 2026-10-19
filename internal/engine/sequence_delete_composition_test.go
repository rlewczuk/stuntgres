package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestSequenceDeleteCompositionPublicationFailure(t *testing.T) {
	for _, phase := range []string{"frontier", "predecessor", "shape", "namespace", "final_cancel"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(0),(1),(1),(2)")
			sessionSQL(t, s, "DELETE FROM t WHERE v=0")
			sessionSQL(t, s, "CREATE SEQUENCE s")
			sequenceScalar(t, s, "SELECT nextval('s')", 1)
			table := catalogRef(t, e, e.CatalogRead(), "t")
			sequence := catalogRef(t, e, e.CatalogRead(), "s")
			healthyFrontier, healthyNamespace := e.visibility.commitSeq, e.catalog.state.NamespaceGeneration()
			ref := transactionBegin(t, s)
			sessionSQL(t, s, "ALTER SEQUENCE s RESTART 100")
			if r := sessionSQL(t, s, "DELETE FROM t WHERE v=1"); r.Count != 2 {
				t.Fatal(r)
			}
			sessionSQL(t, s, "INSERT INTO t VALUES(3),(5)")
			sessionSQL(t, s, "DELETE FROM t WHERE v=5")
			if phase == "namespace" {
				// No TEMP object has ever committed in this session: this is a
				// genuine new namespace descriptor, not another temp relation.
				sessionSQL(t, s, "CREATE TEMP SEQUENCE first_temp START 40")
				state := catalog.CloneState(e.catalog.state)
				state.SetNamespaceGeneration(^uint64(0))
				e.catalog.state = state
			}
			switch phase {
			case "frontier":
				e.visibility.commitSeq = ids.CommitSeq(^uint64(0))
			case "predecessor":
				for id, candidate := range s.transactions.deletes[table.Address].Rows {
					candidate.Version++
					s.transactions.deletes[table.Address].Rows[id] = candidate
					break
				}
			case "shape":
				// Preserve every row and its width; only shape authority changes.
				replacement := *e.storage.Relations[table.Address]
				e.storage.Relations[table.Address] = &replacement
			}
			assertSequenceDeletePrivateBatch(t, s, table.Address, sequence.Address, phase == "namespace")
			before := captureSequenceDeletePublication(e, s, table.Address)
			ticket := s.transactions.ticket
			if ticket == nil || len(e.locks.Inspect(ticket).Journals[ticket]) == 0 {
				t.Fatal("mixed staging did not acquire transaction resources")
			}
			op := transactionOperation(t, s)
			var cancellation *cancelAtErrCheck
			if phase == "final_cancel" {
				base, cancel := context.WithCancel(op.Context())
				defer cancel()
				// Native Commit: admission Err(1), accepted-end Err(2),
				// preparePrivateCatalog Err(3), final publication Err(4).
				// The existing real-context seam cancels at that final check,
				// after row/end/catalog/generator candidates are prepared.
				cancellation = &cancelAtErrCheck{Context: base, cancel: cancel, at: 4}
				op.ctx = cancellation
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
				e.visibility.commitSeq = healthyFrontier
			}
			if phase == "namespace" {
				state := catalog.CloneState(e.catalog.state)
				state.SetNamespaceGeneration(healthyNamespace)
				e.catalog.state = state
			}
			transactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 1, 2)
			sequenceScalar(t, s, "SELECT currval('s')", 1)
			sequenceScalar(t, s, "SELECT lastval()", 1)
			peer := sessionStart(t, e, nil)
			sequenceScalar(t, peer, "SELECT nextval('s')", 2)
			// A fresh mixed transaction must reacquire the retired resources
			// and successfully publish through the same combined branch.
			next := transactionBegin(t, s)
			sessionSQL(t, s, "ALTER SEQUENCE s RESTART 200")
			sessionSQL(t, s, "DELETE FROM t WHERE v=2")
			sessionSQL(t, s, "INSERT INTO t VALUES(9)")
			if phase == "namespace" {
				sessionSQL(t, s, "CREATE TEMP SEQUENCE first_temp START 40")
			}
			frontier := e.visibility.commitSeq
			if r := transactionEnd(t, s, next, true); !r.Committed || e.visibility.commitSeq != frontier+1 {
				t.Fatal("healthy combined transaction failed", r, e.visibility.commitSeq, frontier)
			}
			transactionRows(t, peer, "SELECT v FROM t ORDER BY v", 1, 1, 9)
			sequenceScalar(t, peer, "SELECT nextval('s')", 200)
			if phase == "namespace" {
				sequenceScalar(t, s, "SELECT nextval('first_temp')", 40)
			}
			lockEmpty(t, e)
		})
	}
}

func TestSequenceDeleteCompositionSingleFrontier(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1),(1),(2)")
	sessionSQL(t, s, "CREATE SEQUENCE s")
	sequenceScalar(t, s, "SELECT nextval('s')", 1)
	table := catalogRef(t, e, e.CatalogRead(), "t")
	frontier := e.visibility.commitSeq
	ref := transactionBegin(t, s)
	sessionSQL(t, s, "ALTER SEQUENCE s RESTART 100")
	sessionSQL(t, s, "DELETE FROM t WHERE v=1")
	sessionSQL(t, s, "INSERT INTO t VALUES(3),(5)")
	sessionSQL(t, s, "DELETE FROM t WHERE v=5")
	op := transactionOperation(t, s)
	if _, err := e.Commit(op, ref); err != nil {
		t.Fatal(err)
	}
	store := e.storage.Relations[table.Address]
	if e.visibility.commitSeq != frontier+1 || len(store.Versions) != 5 || store.Ends == nil || len(store.Ends.Rows) != 3 {
		t.Fatal("mixed publication did not install one frontier", e.visibility.commitSeq, frontier, len(store.Versions), store.Ends)
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
	peer := sessionStart(t, e, nil)
	transactionRows(t, peer, "SELECT v FROM t ORDER BY v", 2, 3)
	sequenceScalar(t, peer, "SELECT nextval('s')", 100)
}
