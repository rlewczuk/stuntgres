package tablecmds_test

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func TestVacuumF1SelectionSkipLocked(t *testing.T) {
	e := engine.New()
	owner, holder, worker := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE review_skip(id INT)", "INSERT INTO review_skip VALUES(1),(2)", "DELETE FROM review_skip WHERE id=1"} {
		sessionSQL(t, owner, q)
	}
	ref, store := vacuumStore(t, e, "review_skip")
	root, ends, facts := store.Versions, store.Ends, store.Maintenance
	transactionBegin(t, holder)
	defer sessionSQL(t, holder, "ROLLBACK")
	if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	// Bound a broken blocking selection; success must be genuine NoWait omission.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	op, err := worker.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(op, enginetest.Statement(t, "VACUUM (SKIP_LOCKED) review_skip"), 0, nil)
	notices := op.DetachNotices()
	op.Done()
	if err != nil || result.Count != 0 || result.Rows != nil || result.CommandTag != "VACUUM" {
		t.Errorf("selection must skip, not wait/error: result=%#v err=%v", result, err)
	}
	want := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "55P03", Message: `skipping vacuum of "review_skip" --- lock not available`}
	if len(notices) != 1 || notices[0] != want {
		t.Errorf("full selection diagnostic got %#v want %#v", notices, want)
	}
	if entry := e.Locks.Inspect().Resources[lock.Resource{ObjectRef: ref}]; entry == nil || len(entry.Pending) != 0 {
		t.Errorf("selection left a queued request: %#v", entry)
	}
	if !storage.SameVersionRoot(root, store.Versions) || store.Ends != ends || store.Maintenance != facts || len(e.VacuumProgress) != 0 {
		t.Error("selection skip changed roots, statistics, or progress")
	}
	sessionSQL(t, holder, "ROLLBACK")
	sessionSQL(t, worker, "VACUUM (SKIP_LOCKED) review_skip")
	if store.Maintenance.VacuumCount != 1 || store.Maintenance.Live != 1 || store.Maintenance.Dead != 0 || len(e.VacuumProgress) != 0 {
		t.Fatal("same worker did not recover after holder release", store.Maintenance)
	}
	transactionRows(t, worker, "SELECT id FROM review_skip", 2).Close()
	lockEmpty(t, e)
}

func TestVacuumF2OlderPublisherHorizon(t *testing.T) {
	for _, wrap := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "cross_wrap"}[wrap], func(t *testing.T) {
			for _, phase := range []string{"initial", "empty_vacuum", "older_birth", "older_prepared_birth"} {
				t.Run(phase, func(t *testing.T) {
					e := engine.New()
					if wrap {
						e.Mu.Lock()
						e.Visibility.SetLastAssignedXIDLocked(0xfffffffe)
						e.Mu.Unlock()
					}
					older, creator := sessionStart(t, e, nil), sessionStart(t, e, nil)
					sessionSQL(t, older, "BEGIN ISOLATION LEVEL READ COMMITTED")
					defer sessionSQL(t, older, "ROLLBACK")
					inquiry := sessionSQL(t, older, "SELECT pg_current_xact_id()")
					if inquiry.Rows == nil || inquiry.Count != 1 {
						t.Fatal("assigned inquiry returned no genuine identity", inquiry)
					}
					if err := inquiry.Rows.Close(); err != nil {
						t.Fatal(err)
					}
					e.Mu.Lock()
					assigned := e.Visibility.FullXIDLocked(older.Transactions.Ref)
					e.Mu.Unlock()
					if assigned < 3 {
						t.Fatal("inquiry failed to assign canonical publisher", assigned)
					}
					sessionSQL(t, creator, "CREATE TABLE review_birth(id INT)")
					_, store := vacuumStore(t, e, "review_birth")
					if phase != "initial" {
						sessionSQL(t, creator, "VACUUM (FREEZE) review_birth")
					}
					if phase == "older_birth" || phase == "older_prepared_birth" {
						sessionSQL(t, older, "INSERT INTO review_birth VALUES(7)")
						e.Mu.Lock()
						stillAssigned := e.Visibility.FullXIDLocked(older.Transactions.Ref)
						e.Mu.Unlock()
						if stillAssigned != assigned {
							t.Fatal("older transaction changed canonical identity", assigned, stillAssigned)
						}
						if phase == "older_prepared_birth" {
							sessionSQL(t, older, "PREPARE TRANSACTION 'review_birth'")
							defer func() {
								e.Mu.Lock()
								pending := core.EnginePreparedRef(e).Lookup("review_birth") != nil
								e.Mu.Unlock()
								if pending {
									sessionSQL(t, creator, "ROLLBACK PREPARED 'review_birth'")
								}
							}()
							sessionSQL(t, creator, "VACUUM (FREEZE) review_birth")
							sessionSQL(t, creator, "COMMIT PREPARED 'review_birth'")
						} else {
							sessionSQL(t, older, "COMMIT")
						}
						if len(store.Versions) != 1 {
							t.Fatal("missing committed birth", store.Versions)
						}
						row := store.Versions[0]
						if row.InsertFullXID != assigned || row.Frozen || row.ImportFrozen || row.Born == 0 {
							t.Fatal("birth must retain real unfrozen older publisher", row, assigned)
						}
						// No main-table VACUUM is allowed to repair the invariant before proof.
						sessionSQL(t, creator, "VACUUM (ONLY_DATABASE_STATS)")
						if e.DatabaseFrozenXID > assigned {
							t.Errorf("database horizon %d passed older birth %d", e.DatabaseFrozenXID, assigned)
						}
					}
					if store.Maintenance.FrozenXID < 3 || store.Maintenance.FrozenXID > assigned {
						t.Errorf("%s relation horizon %d must not pass genuine older publisher %d", phase, store.Maintenance.FrozenXID, assigned)
					}
				})
			}
		})
	}
}

func TestVacuumF3ImportedStatistics(t *testing.T) {
	for _, mode := range []string{"foreign", "self", "restore"} {
		t.Run(mode, func(t *testing.T) {
			source, target := engine.New(), engine.New()
			ss := sessionStart(t, source, nil)
			for _, q := range []string{"CREATE TABLE review_import(id INT)", "INSERT INTO review_import VALUES(1),(2)", "VACUUM review_import"} {
				sessionSQL(t, ss, q)
			}
			if mode == "self" {
				target = source
			} else {
				prior := sessionStart(t, target, nil)
				for _, q := range []string{"CREATE TABLE review_import(id INT)", "INSERT INTO review_import VALUES(9)", "VACUUM review_import"} {
					sessionSQL(t, prior, q)
				}
			}
			if mode == "restore" {
				if err := engine.Restore(context.Background(), target, strings.NewReader("CREATE TABLE review_import(id INT); INSERT INTO review_import VALUES(1),(2);")); err != nil {
					t.Fatal(err)
				}
			} else if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			reader := sessionStart(t, target, nil)
			_, store := vacuumStore(t, target, "review_import")
			// Current cardinality is not historical cumulative activity. No VACUUM on
			// this imported object may mask its initialization before these assertions.
			check := func(phase string, live, dead, inserts, deletes, mods, since int64) {
				t.Helper()
				m := store.Maintenance
				if m.Live != live || m.Dead != dead || m.Inserts != inserts || m.Updates != 0 || m.Deletes != deletes || m.Modifications != mods || m.InsertsSince != since || m.Scanned || m.VacuumCount != 0 || !m.LastVacuum.IsZero() {
					t.Errorf("%s current/history facts got %#v; want live/dead=%d/%d ins/del/mod/since=%d/%d/%d/%d with unscanned/reset history", phase, m, live, dead, inserts, deletes, mods, since)
				}
				got := enginetest.Tuples(sessionSQL(t, reader, "SELECT n_live_tup,n_dead_tup,n_tup_ins,n_tup_upd,n_tup_del,n_mod_since_analyze,n_ins_since_vacuum,vacuum_count,last_vacuum IS NULL FROM pg_stat_all_tables WHERE relid='review_import'::regclass"))
				want := [][]driver.Value{{live, dead, inserts, int64(0), deletes, mods, since, int64(0), true}}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s registered provider got %#v want %#v", phase, got, want)
				}
				projected := enginetest.Tuples(sessionSQL(t, reader, "SELECT reltuples=-1::real AS unknown FROM pg_class WHERE oid='review_import'::regclass"))
				if !reflect.DeepEqual(projected, [][]driver.Value{{true}}) {
					t.Errorf("%s imported object must remain unscanned: %#v", phase, projected)
				}
			}
			check("before vacuum or DML", 2, 0, 0, 0, 0, 0)
			transactionRows(t, reader, "SELECT id FROM review_import ORDER BY id", 1, 2).Close()
			sessionSQL(t, reader, "DELETE FROM review_import WHERE id=1")
			check("after delete", 1, 1, 0, 1, 1, 0)
			sessionSQL(t, reader, "INSERT INTO review_import VALUES(7)")
			check("after insert", 2, 1, 1, 1, 2, 1)
			transactionRows(t, reader, "SELECT id FROM review_import ORDER BY id", 2, 7).Close()
		})
	}
}
