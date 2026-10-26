package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConcurrentIndexCancellation(t *testing.T) {
	for _, phase := range []string{"before", "first", "snapshot"} {
		for _, timeout := range []bool{false, true} {
			t.Run(phase+fmtConcurrentCase(timeout, false, false), func(t *testing.T) {
				fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
					ap, bp := fixture.ConcurrentIndexPID(t, p, b, a), fixture.ConcurrentIndexPID(t, p, b, builder)
					fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
					fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
					fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN", 0)
					if phase == "before" {
						fixture.ConcurrentIndexExec(t, p, b, a, "LOCK TABLE t IN SHARE MODE", 0)
					}
					if phase == "first" {
						fixture.ConcurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES(2)", 1)
					}
					if phase == "snapshot" {
						fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE u(n INTEGER)", 0)
						fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO u VALUES(9)", 1)
						fixture.ConcurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
						fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
						fixture.ConcurrentIndexRows(t, p, b, a, "SELECT n FROM u ORDER BY n", 9)
					}
					// Only the held execution runs under the timer. A 100 ms timer
					// leaves no room for an observer: the exact 57014 and the index
					// state below prove the wait, and the cancel variant keeps the
					// strict admission observer.
					arm := ""
					if timeout {
						arm = "SET statement_timeout='100ms'"
					}
					w := fixture.ConcurrentIndexStartTimed(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", arm)
					switch {
					case timeout:
					case phase == "before":
						if b.PG {
							fixture.LockWaitBehind(t, p.Ctx, bp, ap, fixture.LockOID(t, p.Ctx, "t"), "ShareUpdateExclusiveLock")
						}
					default:
						state := stuntgres.IndexState{Live: true}
						wait := "waiting for writers before build"
						if phase == "snapshot" {
							state.Ready = true
							wait = "waiting for old snapshots"
						}
						fixture.ConcurrentIndexWait(t, p, b, "ui", state, bp, ap, wait)
					}
					if !timeout {
						if b.PG {
							var canceled bool
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
								t.Fatalf("cancel: %v %v", canceled, err)
							}
						} else {
							w.Cancel()
						}
					}
					err := w.Join(t, p)
					if timeout {
						want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
						fixture.AssertDiagnostic(t, err, &want)
					} else if b.PG {
						want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
						fixture.AssertDiagnostic(t, err, &want)
					} else if !errors.Is(err, context.Canceled) {
						t.Fatalf("caller cancellation: %v", err)
					}
					if phase == "before" {
						if _, found := fixture.ConcurrentIndexState(t, p, b, "ui"); found {
							t.Fatal("prepublication cancellation left index")
						}
					} else {
						want := stuntgres.IndexState{Live: true}
						if phase == "snapshot" {
							want.Ready = true
						}
						fixture.ConcurrentIndexWait(t, p, b, "ui", want, 0, 0, "")
						if phase == "snapshot" {
							fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(1)", fixture.ConcurrentIndexUnique("t", "ui", "Key (n)=(1) already exists.", false))
						} else {
							fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
						}
						if phase == "first" {
							fixture.ConcurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
						}
						fixture.ConcurrentIndexExec(t, p, b, c, "DROP INDEX ui", 0)
						if !b.PG {
							var out fixture.ConcurrentIndexCountingWriter
							if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil || out.Calls == 0 || out.Bytes == 0 {
								t.Fatal("post-drop Dump", err, out)
							}
						}
					}
					if phase != "first" {
						fixture.ConcurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
					}
					final := builder
					if timeout {
						// The armed timer also bounds the SET that would disarm it,
						// so the armed builder is discarded instead of reset.
						if err := builder.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
							t.Fatal("discarding timer-armed builder", err)
						}
						final = c
					} else {
						fixture.ConcurrentIndexExec(t, p, b, builder, "SET statement_timeout=0", 0)
					}
					fixture.ConcurrentIndexExec(t, p, b, final, "CREATE INDEX CONCURRENTLY ui ON t(n)", 0)
				})
			})
		}
	}
	t.Run("lock-timeout", func(t *testing.T) {
		fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, c := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
			fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
			fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN", 0)
			fixture.ConcurrentIndexExec(t, p, b, a, "LOCK TABLE t IN SHARE MODE", 0)
			fixture.ConcurrentIndexExec(t, p, b, c, "SET lock_timeout='10ms'", 0)
			fixture.ConcurrentIndexError(t, p, b, c, "CREATE INDEX CONCURRENTLY ix ON t(n)", fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout"))
			if _, found := fixture.ConcurrentIndexState(t, p, b, "ix"); found {
				t.Fatal("lock-timeout index exists")
			}
			fixture.ConcurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
			fixture.ConcurrentIndexExec(t, p, b, c, "SET lock_timeout=0", 0)
			fixture.ConcurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY ix ON t(n)", 0)
		})
	})
}

func TestReferenceConcurrentIndexSerialization(t *testing.T) {
	fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
		ap, bp := fixture.ConcurrentIndexPID(t, p, b, a), fixture.ConcurrentIndexPID(t, p, b, builder)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE u(n INTEGER)", 0)
		fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
		fixture.ConcurrentIndexRows(t, p, b, a, "SELECT n FROM u ORDER BY n")
		w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", false)
		fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Live: true}, bp, ap, "waiting for old snapshots")
		fixture.ConcurrentIndexExec(t, p, b, c, "SET lock_timeout='10ms'", 0)
		want := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
		fixture.ConcurrentIndexError(t, p, b, c, "CREATE INDEX CONCURRENTLY iy ON t(n)", want)
		if _, found := fixture.ConcurrentIndexState(t, p, b, "iy"); found {
			t.Fatal("serialized builder published")
		}
		fixture.ConcurrentIndexError(t, p, b, c, "ALTER TABLE t ADD COLUMN extra INTEGER", want)
		fixture.ConcurrentIndexExec(t, p, b, a, "COMMIT", 0)
		if err := w.Join(t, p); err != nil {
			t.Fatal(err)
		}
		fixture.ConcurrentIndexExec(t, p, b, c, "ALTER TABLE t ADD COLUMN extra INTEGER", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY iy ON t(n)", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY ui ON u(n)", 0)
	})
}

func TestReferenceConcurrentIndexSerializationDifferentTables(t *testing.T) {
	fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
		ap, bp := fixture.ConcurrentIndexPID(t, p, b, a), fixture.ConcurrentIndexPID(t, p, b, builder)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE u(n INTEGER)", 0)
		fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL READ COMMITTED", 0)
		fixture.ConcurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES(1)", 1)
		w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", false)
		fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Live: true}, bp, ap, "waiting for writers before build")
		// No ordinary stable snapshot pins the unrelated builder. Target SUE and
		// captured target writer completion must not serialize unrelated-table builds.
		fixture.ConcurrentIndexExec(t, p, b, c, "SET lock_timeout='100ms'", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY ui ON u(n)", 0)
		fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
		fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Live: true}, bp, ap, "waiting for writers before build")
		fixture.ConcurrentIndexExec(t, p, b, a, "COMMIT", 0)
		if err := w.Join(t, p); err != nil {
			t.Fatal(err)
		}
		fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
		fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1)
	})
}
