package reference_test

import (
	"context"
	"errors"
	"stuntgres"
	"testing"
)

func TestReferenceConcurrentIndexCancellation(t *testing.T) {
	for _, phase := range []string{"before", "first", "snapshot"} {
		for _, timeout := range []bool{false, true} {
			t.Run(phase+fmtConcurrentCase(timeout, false, false), func(t *testing.T) {
				concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					a, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
					ap, bp := concurrentIndexPID(t, p, b, a), concurrentIndexPID(t, p, b, builder)
					concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
					concurrentIndexExec(t, p, b, a, "BEGIN", 0)
					if phase == "before" {
						concurrentIndexExec(t, p, b, a, "LOCK TABLE t IN SHARE MODE", 0)
					}
					if phase == "first" {
						concurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES(2)", 1)
					}
					if phase == "snapshot" {
						concurrentIndexExec(t, p, b, c, "CREATE TABLE u(n INTEGER)", 0)
						concurrentIndexExec(t, p, b, c, "INSERT INTO u VALUES(9)", 1)
						concurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
						concurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
						concurrentIndexRows(t, p, b, a, "SELECT n FROM u ORDER BY n", 9)
					}
					if timeout {
						concurrentIndexExec(t, p, b, builder, "SET statement_timeout='2000ms'", 0)
					}
					w := concurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", true)
					if phase == "before" {
						if b.pg {
							referenceLockWaitBehind(t, p.ctx, bp, ap, referenceLockOID(t, p.ctx, "t"), "ShareUpdateExclusiveLock")
						}
					} else {
						state := stuntgres.IndexState{Live: true}
						wait := "waiting for writers before build"
						if phase == "snapshot" {
							state.Ready = true
							wait = "waiting for old snapshots"
						}
						concurrentIndexWait(t, p, b, "ui", state, bp, ap, wait)
					}
					if !timeout {
						if b.pg {
							var canceled bool
							if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
								t.Fatalf("cancel: %v %v", canceled, err)
							}
						} else {
							w.cancel()
						}
					}
					err := w.join(t, p)
					if timeout {
						want := controlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
						referenceAssertDiagnostic(t, err, &want)
					} else if b.pg {
						want := controlsReferenceDiagnostic("57014", "canceling statement due to user request")
						referenceAssertDiagnostic(t, err, &want)
					} else if !errors.Is(err, context.Canceled) {
						t.Fatalf("caller cancellation: %v", err)
					}
					if phase == "before" {
						if _, found := concurrentIndexState(t, p, b, "ui"); found {
							t.Fatal("prepublication cancellation left index")
						}
					} else {
						want := stuntgres.IndexState{Live: true}
						if phase == "snapshot" {
							want.Ready = true
						}
						concurrentIndexWait(t, p, b, "ui", want, 0, 0, "")
						if phase == "snapshot" {
							concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(1)", concurrentIndexUnique("t", "ui", "Key (n)=(1) already exists.", false))
						} else {
							concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
						}
						if phase == "first" {
							concurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
						}
						concurrentIndexExec(t, p, b, c, "DROP INDEX ui", 0)
						if !b.pg {
							var out concurrentIndexCountingWriter
							if err := stuntgres.Dump(p.ctx, p.native, &out); err != nil || out.calls == 0 || out.bytes == 0 {
								t.Fatal("post-drop Dump", err, out)
							}
						}
					}
					if phase != "first" {
						concurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
					}
					concurrentIndexExec(t, p, b, builder, "SET statement_timeout=0", 0)
					concurrentIndexExec(t, p, b, builder, "CREATE INDEX CONCURRENTLY ui ON t(n)", 0)
				})
			})
		}
	}
	t.Run("lock-timeout", func(t *testing.T) {
		concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
			a, c := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
			concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
			concurrentIndexExec(t, p, b, a, "BEGIN", 0)
			concurrentIndexExec(t, p, b, a, "LOCK TABLE t IN SHARE MODE", 0)
			concurrentIndexExec(t, p, b, c, "SET lock_timeout='100ms'", 0)
			concurrentIndexError(t, p, b, c, "CREATE INDEX CONCURRENTLY ix ON t(n)", controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout"))
			if _, found := concurrentIndexState(t, p, b, "ix"); found {
				t.Fatal("lock-timeout index exists")
			}
			concurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
			concurrentIndexExec(t, p, b, c, "SET lock_timeout=0", 0)
			concurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY ix ON t(n)", 0)
		})
	})
}

func TestReferenceConcurrentIndexSerialization(t *testing.T) {
	concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
		ap, bp := concurrentIndexPID(t, p, b, a), concurrentIndexPID(t, p, b, builder)
		concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		concurrentIndexExec(t, p, b, c, "CREATE TABLE u(n INTEGER)", 0)
		concurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
		concurrentIndexRows(t, p, b, a, "SELECT n FROM u ORDER BY n")
		w := concurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", false)
		concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Live: true}, bp, ap, "waiting for old snapshots")
		concurrentIndexExec(t, p, b, c, "SET lock_timeout='100ms'", 0)
		want := controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
		concurrentIndexError(t, p, b, c, "CREATE INDEX CONCURRENTLY iy ON t(n)", want)
		if _, found := concurrentIndexState(t, p, b, "iy"); found {
			t.Fatal("serialized builder published")
		}
		concurrentIndexError(t, p, b, c, "ALTER TABLE t ADD COLUMN extra INTEGER", want)
		concurrentIndexExec(t, p, b, a, "COMMIT", 0)
		if err := w.join(t, p); err != nil {
			t.Fatal(err)
		}
		concurrentIndexExec(t, p, b, c, "ALTER TABLE t ADD COLUMN extra INTEGER", 0)
		concurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY iy ON t(n)", 0)
		concurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY ui ON u(n)", 0)
	})
}

func TestReferenceConcurrentIndexSerializationDifferentTables(t *testing.T) {
	concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
		ap, bp := concurrentIndexPID(t, p, b, a), concurrentIndexPID(t, p, b, builder)
		concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		concurrentIndexExec(t, p, b, c, "CREATE TABLE u(n INTEGER)", 0)
		concurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL READ COMMITTED", 0)
		concurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES(1)", 1)
		w := concurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", false)
		concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Live: true}, bp, ap, "waiting for writers before build")
		// No ordinary stable snapshot pins the unrelated builder. Target SUE and
		// captured target writer completion must not serialize unrelated-table builds.
		concurrentIndexExec(t, p, b, c, "SET lock_timeout='100ms'", 0)
		concurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY ui ON u(n)", 0)
		concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
		concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Live: true}, bp, ap, "waiting for writers before build")
		concurrentIndexExec(t, p, b, a, "COMMIT", 0)
		if err := w.join(t, p); err != nil {
			t.Fatal(err)
		}
		concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
		concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1)
	})
}
