package tablecmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

// Observe the real manager while the initiating explicit transaction remains
// alive: absence of allocation alone would not prove absence of sequence RX.
func TestSequenceDeleteUndemandedLocks(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{"DELETE FROM empty_t WHERE nextval('s')>0", "DELETE FROM t WHERE false AND nextval(NULL::regclass)>0"} {
			t.Run(q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e := engine.New()
				a := enginetest.StartSession(t, e, nil)
				b := enginetest.StartSession(t, e, nil)
				for _, sql := range []string{"CREATE TABLE empty_t(id INT)", "CREATE TABLE t(id INT)", "INSERT INTO t VALUES(1)", "CREATE SEQUENCE s START 31 CACHE 1"} {
					enginetest.SessionSQL(t, a, sql)
				}
				sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "s")
				targetName := "t"
				if q == "DELETE FROM empty_t WHERE nextval('s')>0" {
					targetName = "empty_t"
				}
				target := enginetest.CatalogRef(t, e, e.CatalogRead(), targetName)
				tx := enginetest.TransactionBegin(t, a)
				if prepared {
					ast, description := sequencePreparedDescription(t, e, a, q)
					result, err := sequencePreparedExecute(e, a, ast, &description)
					if err != nil || result.Count != 0 || result.Rows != nil {
						t.Fatal(result, err)
					}
				} else {
					deleteCount(t, a, q, 0)
				}
				ticket := a.Transactions.Ticket
				lockState := e.Locks.Inspect(ticket)
				entry := lockState.Resources[lock.Resource{ObjectRef: sequence}]
				sequenceHeld := entry != nil && entry.Holders[ticket].Mode != 0
				_, sequenceJournaled := lockState.Journals[ticket][lock.Resource{ObjectRef: sequence}]
				targetEntry := lockState.Resources[lock.Resource{ObjectRef: target}]
				targetHeld := targetEntry != nil && targetEntry.Holders[ticket].Mode == lock.RowExclusive
				if sequenceHeld || sequenceJournaled || !targetHeld {
					t.Fatal("wrong live transaction grants", sequenceHeld, sequenceJournaled, targetHeld)
				}
				e.Mu.Lock()
				generator := *e.Storage.Sequences[sequence.Address]
				e.Mu.Unlock()
				if generator.Last != 31 || generator.Called {
					t.Fatal("undemanded DELETE allocated", generator)
				}
				// The state check is after lock observation so it cannot create AS
				// and obscure which resources the DELETE itself demanded.
				enginetest.TransactionRows(t, a, "SELECT last_value FROM s", 31)
				enginetest.SessionSQL(t, b, "ALTER SEQUENCE s RESTART 80")
				enginetest.TransactionEnd(t, a, tx, true)
				enginetest.TransactionRows(t, a, "SELECT last_value FROM s", 80)
				enginetest.TransactionRows(t, a, "SELECT nextval('s')", 80)
				enginetest.TransactionRows(t, a, "SELECT id FROM t", 1)
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func TestSequenceDeleteDecisiveFalseDemand(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(1),(1),(2)", "CREATE SEQUENCE s START 101 CACHE 1"} {
		enginetest.SessionSQL(t, a, q)
	}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "s")
	for i, prepared := range []bool{false, true} {
		tx := enginetest.TransactionBegin(t, a)
		q := "DELETE FROM t WHERE false AND nextval('s')>0"
		if prepared {
			ast, description := sequencePreparedDescription(t, e, a, q)
			r, err := sequencePreparedExecute(e, a, ast, &description)
			if err != nil || r.Count != 0 || r.Rows != nil {
				t.Fatal(r, err)
			}
		} else {
			deleteCount(t, a, q, 0)
		}
		ticket := a.Transactions.Ticket
		lockState := e.Locks.Inspect(ticket)
		entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
		held := entry != nil && entry.Holders[ticket].Mode == lock.RowExclusive
		_, journaled := lockState.Journals[ticket][lock.Resource{ObjectRef: ref}]
		if !held || !journaled {
			t.Fatal("demanded per-row nextval lacks sequence RX", held, journaled)
		}
		want := int64(103 + i*3)
		e.Mu.Lock()
		generator := *e.Storage.Sequences[ref.Address]
		e.Mu.Unlock()
		if generator.Last != want || !generator.Called {
			t.Fatal("per-row allocation count", generator, want)
		}
		enginetest.TransactionRows(t, a, "SELECT currval('s')", want)
		enginetest.TransactionRows(t, a, "SELECT lastval()", want)
		enginetest.TransactionEnd(t, a, tx, false)
		enginetest.LockEmpty(t, e)
		enginetest.TransactionRows(t, a, "SELECT id FROM t ORDER BY id", 1, 1, 2)
		enginetest.TransactionRows(t, a, "SELECT currval('s')", want)
	}
	enginetest.TransactionRows(t, a, "SELECT nextval('s')", 107)
}

func deleteCount(t *testing.T, s *session.Session, query string, want int64) {
	t.Helper()
	result := enginetest.SessionSQL(t, s, query)
	if result.Count != want || result.Rows != nil {
		t.Fatalf("%s: count=%d rows=%v, want %d/no relation", query, result.Count, result.Rows, want)
	}
}
