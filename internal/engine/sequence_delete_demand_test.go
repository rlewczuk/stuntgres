package engine

import (
	"testing"

	"stuntgres/internal/engine/lock"
)

// Observe the real manager while the initiating explicit transaction remains
// alive: absence of allocation alone would not prove absence of sequence RX.
func TestSequenceDeleteUndemandedLocks(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{"DELETE FROM empty_t WHERE nextval('s')>0", "DELETE FROM t WHERE false AND nextval(NULL::regclass)>0"} {
			t.Run(q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e := New()
				a := sessionStart(t, e, nil)
				b := sessionStart(t, e, nil)
				for _, sql := range []string{"CREATE TABLE empty_t(id INT)", "CREATE TABLE t(id INT)", "INSERT INTO t VALUES(1)", "CREATE SEQUENCE s START 31 CACHE 1"} {
					sessionSQL(t, a, sql)
				}
				sequence := catalogRef(t, e, e.CatalogRead(), "s")
				targetName := "t"
				if q == "DELETE FROM empty_t WHERE nextval('s')>0" {
					targetName = "empty_t"
				}
				target := catalogRef(t, e, e.CatalogRead(), targetName)
				tx := transactionBegin(t, a)
				if prepared {
					ast, description := sequencePreparedDescription(t, e, a, q)
					result, err := sequencePreparedExecute(e, a, ast, &description)
					if err != nil || result.Count != 0 || result.Rows != nil {
						t.Fatal(result, err)
					}
				} else {
					deleteCount(t, a, q, 0)
				}
				ticket := a.transactions.ticket
				lockState := e.locks.Inspect(ticket)
				entry := lockState.Resources[lock.Resource{ObjectRef: sequence}]
				sequenceHeld := entry != nil && entry.Holders[ticket].Mode != 0
				_, sequenceJournaled := lockState.Journals[ticket][lock.Resource{ObjectRef: sequence}]
				targetEntry := lockState.Resources[lock.Resource{ObjectRef: target}]
				targetHeld := targetEntry != nil && targetEntry.Holders[ticket].Mode == lock.RowExclusive
				if sequenceHeld || sequenceJournaled || !targetHeld {
					t.Fatal("wrong live transaction grants", sequenceHeld, sequenceJournaled, targetHeld)
				}
				e.mu.Lock()
				generator := *e.sequences[sequence.Address]
				e.mu.Unlock()
				if generator.last != 31 || generator.called {
					t.Fatal("undemanded DELETE allocated", generator)
				}
				// The state check is after lock observation so it cannot create AS
				// and obscure which resources the DELETE itself demanded.
				transactionRows(t, a, "SELECT last_value FROM s", 31)
				sessionSQL(t, b, "ALTER SEQUENCE s RESTART 80")
				transactionEnd(t, a, tx, true)
				transactionRows(t, a, "SELECT last_value FROM s", 80)
				transactionRows(t, a, "SELECT nextval('s')", 80)
				transactionRows(t, a, "SELECT id FROM t", 1)
				lockEmpty(t, e)
			})
		}
	}
}

func TestSequenceDeleteDecisiveFalseDemand(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(1),(1),(2)", "CREATE SEQUENCE s START 101 CACHE 1"} {
		sessionSQL(t, a, q)
	}
	ref := catalogRef(t, e, e.CatalogRead(), "s")
	for i, prepared := range []bool{false, true} {
		tx := transactionBegin(t, a)
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
		ticket := a.transactions.ticket
		lockState := e.locks.Inspect(ticket)
		entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
		held := entry != nil && entry.Holders[ticket].Mode == lock.RowExclusive
		_, journaled := lockState.Journals[ticket][lock.Resource{ObjectRef: ref}]
		if !held || !journaled {
			t.Fatal("demanded per-row nextval lacks sequence RX", held, journaled)
		}
		want := int64(103 + i*3)
		e.mu.Lock()
		generator := *e.sequences[ref.Address]
		e.mu.Unlock()
		if generator.last != want || !generator.called {
			t.Fatal("per-row allocation count", generator, want)
		}
		transactionRows(t, a, "SELECT currval('s')", want)
		transactionRows(t, a, "SELECT lastval()", want)
		transactionEnd(t, a, tx, false)
		lockEmpty(t, e)
		transactionRows(t, a, "SELECT id FROM t ORDER BY id", 1, 1, 2)
		transactionRows(t, a, "SELECT currval('s')", want)
	}
	transactionRows(t, a, "SELECT nextval('s')", 107)
}
