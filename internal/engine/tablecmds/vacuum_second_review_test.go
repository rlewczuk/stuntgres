package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func TestVacuumF4FutureRelationDatabaseHorizon(t *testing.T) {
	for _, wrap := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "cross_wrap"}[wrap], func(t *testing.T) {
			e := engine.New()
			if wrap {
				e.Mu.Lock()
				e.Visibility.SetLastAssignedXIDLocked(0xfffffffe)
				e.Mu.Unlock()
			}
			older, creator := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, older, "BEGIN ISOLATION LEVEL READ COMMITTED")
			defer enginetest.SessionSQL(t, older, "ROLLBACK")
			inquiry := enginetest.SessionSQL(t, older, "SELECT pg_current_xact_id()")
			if inquiry.Rows == nil || inquiry.Count != 1 {
				t.Fatal("no real assigned inquiry", inquiry)
			}
			if err := inquiry.Rows.Close(); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			assigned := e.Visibility.FullXIDLocked(older.Transactions.Ref)
			e.Mu.Unlock()
			if assigned < 3 {
				t.Fatal("inquiry did not assign a canonical publisher", assigned)
			}
			// No heap exists yet to hide the database calculation's seed.
			enginetest.SessionSQL(t, creator, "VACUUM (ONLY_DATABASE_STATS)")
			if db := e.DatabaseFrozenXID; db < 3 || db > assigned {
				t.Errorf("before CREATE database bound %d passed active publisher %d", db, assigned)
			}
			enginetest.SessionSQL(t, creator, "CREATE TABLE future_birth(id INT)")
			_, store := vacuumStore(t, e, "future_birth")
			check := func(phase string) {
				t.Helper()
				f, db := store.Maintenance.FrozenXID, e.DatabaseFrozenXID
				if f < 3 || f > assigned {
					t.Errorf("%s relation bound %d passed older publisher %d", phase, f, assigned)
				}
				if db < 3 || db > assigned || db > f {
					t.Errorf("%s database bound %d must not pass relation %d or older publisher %d", phase, db, f, assigned)
				}
				if store.Maintenance.VacuumCount != 0 || !store.Maintenance.LastVacuum.IsZero() {
					t.Error("unexpected repair maintenance", store.Maintenance)
				}
			}
			check("after CREATE before birth")
			enginetest.SessionSQL(t, older, "INSERT INTO future_birth VALUES(7)")
			e.Mu.Lock()
			stillAssigned := e.Visibility.FullXIDLocked(older.Transactions.Ref)
			e.Mu.Unlock()
			if stillAssigned != assigned {
				t.Fatal("transaction changed actual publisher", assigned, stillAssigned)
			}
			enginetest.SessionSQL(t, older, "COMMIT")
			if len(store.Versions) != 1 {
				t.Fatal("missing older committed birth", store.Versions)
			}
			row := store.Versions[0]
			if row.InsertFullXID != assigned || row.Frozen || row.ImportFrozen || row.Born == 0 {
				t.Fatal("not a genuine unfrozen older birth", row, assigned)
			}
			check("after older birth before any repair")
			enginetest.TransactionRows(t, creator, "SELECT id FROM future_birth", 7).Close()
			enginetest.SessionSQL(t, creator, "VACUUM (FREEZE) future_birth")
			enginetest.TransactionRows(t, creator, "SELECT id FROM future_birth", 7).Close()
			if store.Maintenance.VacuumCount != 1 || len(e.VacuumProgress) != 0 {
				t.Fatal("post-proof real maintenance did not complete", store.Maintenance)
			}
		})
	}
}

func TestVacuumF5SelectionNoticeOrder(t *testing.T) {
	for _, kind := range []struct{ name, target, message string }{
		{"permission", "notice_denied", `permission denied to vacuum "notice_denied", skipping it`},
		{"nonheap", "notice_special", `skipping "notice_special" --- cannot vacuum non-tables or special system tables`},
		{"partition_only", "ONLY notice_partition", `VACUUM ONLY of partitioned table "notice_partition" has no effect`},
	} {
		t.Run(kind.name, func(t *testing.T) {
			for _, deniedFirst := range []bool{true, false} {
				t.Run(map[bool]string{true: "denied_then_locked", false: "locked_then_denied"}[deniedFirst], func(t *testing.T) {
					e := engine.New()
					owner, holder, actor := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
					for _, q := range []string{"CREATE ROLE notice_actor", "GRANT USAGE ON SCHEMA public TO notice_actor", "CREATE TABLE notice_denied(id INT)", "CREATE TABLE notice_locked(id INT)", "INSERT INTO notice_denied VALUES(1),(2)", "INSERT INTO notice_locked VALUES(1),(2)", "DELETE FROM notice_denied WHERE id=1", "DELETE FROM notice_locked WHERE id=1", "VACUUM notice_denied,notice_locked", "GRANT MAINTAIN ON notice_locked TO notice_actor"} {
						enginetest.SessionSQL(t, owner, q)
					}
					_, denied := vacuumStore(t, e, "notice_denied")
					lockedRef, locked := vacuumStore(t, e, "notice_locked")
					deniedFacts, lockedFacts := denied.Maintenance, locked.Maintenance
					deniedRoot, lockedRoot := denied.Versions, locked.Versions
					deniedEnds, lockedEnds := denied.Ends, locked.Ends
					enginetest.SessionSQL(t, actor, "SET ROLE notice_actor")
					enginetest.SessionSQL(t, owner, "GRANT CREATE ON SCHEMA public TO notice_actor")
					enginetest.SessionSQL(t, actor, "CREATE VIEW notice_special AS SELECT 1 AS id")
					enginetest.SessionSQL(t, actor, "CREATE TABLE notice_partition(id INT) PARTITION BY RANGE(id)")
					enginetest.TransactionBegin(t, holder)
					defer enginetest.SessionSQL(t, holder, "ROLLBACK")
					if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: lockedRef}, lock.AccessExclusive); err != nil {
						t.Fatal(err)
					}
					selection := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: kind.message}
					unavailable := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "55P03", Message: `skipping vacuum of "notice_locked" --- lock not available`}
					list := kind.target + ",notice_locked"
					want := []sqlstate.Error{selection, unavailable}
					if !deniedFirst {
						list = "notice_locked," + kind.target
						want = []sqlstate.Error{unavailable, selection}
					}
					// Nonheap rejection is work-phase, after ALL expansion notices,
					// irrespective of the textual position of this authorized view.
					if kind.name == "nonheap" {
						want = []sqlstate.Error{unavailable, selection}
					}
					op := enginetest.TransactionOperation(t, actor)
					result, err := e.Execute(op, enginetest.Statement(t, "VACUUM (SKIP_LOCKED) "+list), 0, nil)
					notices := op.DetachNotices()
					op.Done()
					if err != nil || result.CommandTag != "VACUUM" || result.Count != 0 || result.Rows != nil {
						t.Fatal("mixed skips must succeed with no result", result, err)
					}
					if !reflect.DeepEqual(notices, want) {
						t.Errorf("ordered full diagnostics got %#v want %#v", notices, want)
					}
					if denied.Maintenance != deniedFacts || locked.Maintenance != lockedFacts || !storage.SameVersionRoot(deniedRoot, denied.Versions) || !storage.SameVersionRoot(lockedRoot, locked.Versions) || denied.Ends != deniedEnds || locked.Ends != lockedEnds || len(e.VacuumProgress) != 0 {
						t.Error("mixed skips changed roots/statistics/progress")
					}
					if entry := e.Locks.Inspect().Resources[lock.Resource{ObjectRef: lockedRef}]; entry == nil || len(entry.Pending) != 0 {
						t.Error("selection left a queued request", entry)
					}
					failed := enginetest.TransactionOperation(t, actor)
					_, failure := e.Execute(failed, enginetest.Statement(t, "VACUUM (SKIP_LOCKED) "+kind.target+",notice_missing"), 0, nil)
					retained := failed.DetachNotices()
					failed.Done()
					var diagnostic *sqlstate.Error
					if !errors.As(failure, &diagnostic) || diagnostic.Code != "42P01" {
						t.Errorf("later missing target must retain genuine resolution error: %#v", failure)
					}
					var beforeFailure []sqlstate.Error
					if kind.name != "nonheap" {
						beforeFailure = []sqlstate.Error{selection}
					}
					if len(retained) != len(beforeFailure) || len(beforeFailure) != 0 && !reflect.DeepEqual(retained, beforeFailure) {
						t.Errorf("selection failure diagnostics got %#v want %#v; work has not begun", retained, beforeFailure)
					}
					enginetest.SessionSQL(t, holder, "ROLLBACK")
					enginetest.LockEmpty(t, e)
				})
			}
		})
	}
}
