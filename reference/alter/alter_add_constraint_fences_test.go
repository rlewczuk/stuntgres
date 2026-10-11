package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"runtime"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func addConstraintLockFences(t *testing.T) {
	t.Run("queued_provider_acl_reopened", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"add_wait_owner"})
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE add_wait_owner", "CREATE TABLE p(id INT PRIMARY KEY)", "GRANT REFERENCES ON p TO add_wait_owner"} {
				run(fixture.Exec(q, 0))
			}
			if b.PG {
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO add_wait_owner", 0))
			}
			admin, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			run(fixture.Exec("SET ROLE add_wait_owner", 0))
			run(fixture.Exec("CREATE TABLE t(a INT)", 0))
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			q := "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID"
			held := addConstraintHeld(t, p, b, c, q)
			tx, err := admin.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE p IN ROW EXCLUSIVE MODE", 0))
			workerPID, holderPID := 0, 0
			if b.PG {
				workerPID, holderPID = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, admin)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := held.ExecContext(ctx, q); done <- err }()
			joined := false
			defer func() {
				cancel()
				tx.Rollback()
				if !joined {
					select {
					case <-done:
					case <-p.Ctx.Done():
						t.Error("ACL worker did not join", p.Ctx.Err())
					}
				}
			}()
			if b.PG {
				fixture.LockWaitBehind(t, p.Ctx, workerPID, holderPID, fixture.LockOID(t, p.Ctx, "p"), "ShareRowExclusiveLock")
			} else {
				for {
					select {
					case err := <-done:
						joined = true
						t.Fatal("FK finished before provider release", err)
					case <-p.Ctx.Done():
						t.Fatal(p.Ctx.Err())
					default:
					}
					probe, err := observer.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					_, err = probe.ExecContext(p.Ctx, "LOCK TABLE p IN ROW EXCLUSIVE MODE NOWAIT")
					probe.Rollback()
					if err != nil {
						fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: `could not obtain lock on relation "p"`})
						break
					}
					runtime.Gosched()
				}
			}
			// Standalone ACL mutation avoids the native explicit-GRANT transaction boundary.
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Exec("REVOKE REFERENCES ON p FROM add_wait_owner", 0))
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				joined = true
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table p"})
			case <-p.Ctx.Done():
				t.Fatal("grant-time ACL worker did not join", p.Ctx.Err())
			}
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
			run(fixture.Exec("GRANT REFERENCES ON p TO add_wait_owner", 0))
			run(fixture.Exec("SET ROLE add_wait_owner", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
		})
	})
	t.Run("native_catalog_replacement_during_provider_wait", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("INSERT INTO t VALUES(1,'one')", 1))
			if b.PG {
				// PostgreSQL has no native replacement API; ordinary FK remains a real witness.
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID", 0))
				run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(9,'bad')", addConstraintFK("t", "fk", "a", "9", "p")))
				return
			}
			dump := addConstraintDump(t, p)
			holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			tx, err := holder.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE p IN ROW EXCLUSIVE MODE", 0))
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := c.ExecContext(ctx, "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID")
				done <- err
			}()
			joined := false
			defer func() {
				cancel()
				tx.Rollback()
				if !joined {
					select {
					case <-done:
					case <-p.Ctx.Done():
						t.Error("replacement worker did not join", p.Ctx.Err())
					}
				}
			}()
			for {
				select {
				case err := <-done:
					joined = true
					t.Fatal("FK finished before provider release", err)
				case <-p.Ctx.Done():
					t.Fatal(p.Ctx.Err())
				default:
				}
				probe, err := observer.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				_, err = probe.ExecContext(p.Ctx, "LOCK TABLE p IN ROW EXCLUSIVE MODE NOWAIT")
				probe.Rollback()
				if err != nil {
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: `could not obtain lock on relation "p"`})
					break
				}
				runtime.Gosched()
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			// Replacement retires old owner/storage/holds. A stale ADD cannot retarget.
			select {
			case err := <-done:
				joined = true
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			case <-p.Ctx.Done():
				t.Fatal("replacement worker did not join", p.Ctx.Err())
			}
			tx.Rollback()
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
			run(addConstraintRows([]any{int64(1), "one"}))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID", 0))
		})
	})
}
