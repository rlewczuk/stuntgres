package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

func TestDeferredUniqueBatchIdentity(t *testing.T) {
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		for _, repair := range []string{"DELETE FROM t WHERE id=2", "UPDATE t SET v=2 WHERE id=2"} {
			t.Run(kind+"/"+repair, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, s, "CREATE TABLE t(id INT,v INT,CONSTRAINT u "+kind+"(v) INITIALLY DEFERRED)")
				enginetest.TransactionBegin(t, s)
				enginetest.SessionSQL(t, s, "CREATE TABLE private_marker(v INT)")
				enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1,1),(2,1)")
				ref := enginetest.RoleTableRef(t, e, "t")
				idx := enginetest.RoleTableRef(t, e, "u")
				writes := s.Transactions.Writes[ref.Address]
				projection := e.Exclusions.OwnLocked(s.Transactions.Ticket)
				if projection == nil {
					t.Fatal("batch lost dirty admission owner")
				}
				entries := projection.Entries[idx.Address]
				if len(writes) != 2 || len(entries) != 2 {
					t.Fatal("batch lost physical or projected rows", writes, entries)
				}
				for _, row := range writes {
					found := false
					for _, entry := range entries {
						if entry.Row.Row == row.Row && entry.Row.Version == row.Version {
							found = true
							break
						}
					}
					if !found {
						t.Fatal("batch allocated a second identity instead of staging reserved incarnation", row, entries)
					}
				}
				modes := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
				if len(modes.Events) != 1 || modes.Events[0].Row != writes[1].Row || modes.Events[0].Version != writes[1].Version {
					t.Fatal("potential conflict event does not name the physical later row", modes.Events, writes)
				}
				enginetest.SessionSQL(t, s, "SAVEPOINT m")
				enginetest.SessionSQL(t, s, repair)
				if e.Exclusions.OwnLocked(s.Transactions.Ticket) == projection {
					t.Fatal("repair mutated its saved projection instead of replacing it")
				}
				enginetest.SessionSQL(t, s, "SET CONSTRAINTS u IMMEDIATE")
				if len(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions)).Events) != 0 {
					t.Fatal("repair failed to retire the original physical event")
				}
				enginetest.SessionSQL(t, s, "ROLLBACK TO m")
				if e.Exclusions.OwnLocked(s.Transactions.Ticket) != projection {
					t.Fatal("savepoint lost exact original physical projection")
				}
				enginetest.SessionSQL(t, s, repair)
				enginetest.SessionSQL(t, s, "SET CONSTRAINTS u IMMEDIATE")
				enginetest.SessionSQL(t, s, "COMMIT")
				if e.Exclusions.CountLocked() != 0 {
					t.Fatal("committed batch retained projection ownership")
				}
				enginetest.UniqueClaimsEmpty(t, e)
			})
		}
	}
}
