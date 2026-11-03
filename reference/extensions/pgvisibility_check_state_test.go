package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityCheckTruncate(t *testing.T) {
	pvcCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"INSERT INTO pv_part.heap VALUES(1)", "CREATE TABLE pv_part.other(k integer)", "INSERT INTO pv_part.other VALUES(9)", "VACUUM (FREEZE) pv_part.other"} {
			n := int64(0)
			if q[:6] == "INSERT" {
				n = 1
			}
			pvRun(t, p, b, c, fixture.Exec(q, n))
		}
		pvFreeze(t, p, b, c)
		pvOneFrozen(t, p, b, c)
		pvcTruncate(t, p, b, c, nil)
		pvOneFrozen(t, p, b, c)
		q := "SELECT * FROM pv_part.pg_visibility($1::regclass,0)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, pvPageNames, pvPageTypes, [][]any{{true, true, true}}, "pv_part.heap"))
		pvcTruncate(t, p, b, c, "pv_part.heap")
		for range 3 {
			pvcSuppressed(t, p, b, c)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, pvPageNames, pvPageTypes, [][]any{{false, false, true}}, "pv_part.heap"))
			pvBoth(t, p, b, c, fixture.Select("SELECT (pv_part.pg_visibility_map_summary('pv_part.heap')).all_visible AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		fresh := pvConn(t, p, b)
		pvcSuppressed(t, p, b, fresh)
		fresh.Close()
		pvcTruncate(t, p, b, c, nil)
		pvcTruncate(t, p, b, c, "pv_part.heap")
		pvcSuppressed(t, p, b, c)
		pvProbe(t, p, b, c, "pv_part.other", 0, true, true, true)
		// This VACUUM has no tuple xmin/header work left: it must still rebuild VM.
		pvRun(t, p, b, c, fixture.Exec("VACUUM pv_part.heap", 0))
		pvOneFrozen(t, p, b, c)
	})
}

func TestReferencePgvisibilityCheckTransactions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pvc_vacuum_denied"})
	pvcCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pvRun(t, p, b, c, fixture.Exec("INSERT INTO pv_part.heap VALUES(1)", 1))
		pvFreeze(t, p, b, c)
		defer pvRollback(c)
		for _, prefix := range []string{"BEGIN", "BEGIN; SAVEPOINT before_vm"} {
			if prefix == "BEGIN" {
				pvRun(t, p, b, c, fixture.Exec("BEGIN", 0))
			} else {
				pvRun(t, p, b, c, fixture.Exec("BEGIN", 0))
				pvRun(t, p, b, c, fixture.Exec("SAVEPOINT before_vm", 0))
			}
			pvcTruncate(t, p, b, c, "pv_part.heap")
			if prefix != "BEGIN" {
				pvRun(t, p, b, c, fixture.Exec("ROLLBACK TO SAVEPOINT before_vm", 0))
				pvcSuppressed(t, p, b, c)
			}
			pvRun(t, p, b, c, pvError("SELECT 1/0", "22012", "division by zero"))
			pvRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
			pvcSuppressed(t, p, b, c)
			pvRun(t, p, b, c, fixture.Exec("BEGIN", 0))
			s := pvError("VACUUM pv_part.heap", "25001", "VACUUM cannot run inside a transaction block")
			s.SelectRows = false
			pvRun(t, p, b, c, s)
			pvRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
			pvcSuppressed(t, p, b, c)
			pvRun(t, p, b, c, fixture.Exec("VACUUM pv_part.heap", 0))
			pvOneFrozen(t, p, b, c)
		}
		reader := pvConn(t, p, b)
		defer reader.Close()
		defer pvRollback(reader)
		pvRun(t, p, b, reader, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
		pvBoth(t, p, b, reader, fixture.Select("SELECT k FROM pv_part.heap", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		// Truncation's AX is incompatible with the reader's retained table lock;
		// use the same snapshot owner to mutate without changing its SELECT truth.
		pvcChecks(t, p, b, reader, "pv_part.heap", nil)
		pvcTruncate(t, p, b, reader, "pv_part.heap")
		pvcSuppressed(t, p, b, reader)
		pvRun(t, p, b, reader, fixture.Exec("ROLLBACK", 0))
		pvcSuppressed(t, p, b, c)
		// Ordinary pre-canceled context errors are a native API boundary.
		// Fast pq calls can return Rows and retire their transport; shared
		// cancellation/publication proof instead observes an admitted lock wait.
		pvRun(t, p, b, c, fixture.Exec("VACUUM pv_part.heap", 0))
		pvOneFrozen(t, p, b, c)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if !b.PG {
			func() {
				boundary := pvConn(t, p, b)
				defer boundary.Close()
				rows, err := boundary.QueryContext(ctx, "SELECT pv_part.pg_truncate_visibility_map('pv_part.heap')")
				if rows != nil {
					rows.Close()
					t.Fatal("precanceled native mutator returned rows")
				}
				fixture.LexicalOrdinary(t, err, context.Canceled)
			}()
			pvOneFrozen(t, p, b, c)
		}
		pvcTruncate(t, p, b, c, "pv_part.heap")
		if !b.PG {
			func() {
				boundary := pvConn(t, p, b)
				defer boundary.Close()
				_, err := boundary.ExecContext(ctx, "VACUUM pv_part.heap")
				fixture.LexicalOrdinary(t, err, context.Canceled)
			}()
		}
		pvcSuppressed(t, p, b, c)
		var notices []fixture.Diagnostic
		_, worker, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		for _, q := range []string{"CREATE ROLE pvc_vacuum_denied", "GRANT USAGE ON SCHEMA pv_part TO pvc_vacuum_denied", "SET SESSION AUTHORIZATION pvc_vacuum_denied"} {
			fixture.RunStep(t, p.Ctx, worker, rec, fixture.Exec(q, 0))
		}
		notices = nil
		fixture.RunStep(t, p.Ctx, worker, rec, fixture.Exec("VACUUM pv_part.heap", 0))
		want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "permission denied to vacuum \"heap\", skipping it"}
		if len(notices) != 1 || notices[0] != want {
			t.Fatal("unauthorized VACUUM notices", notices, want)
		}
		fixture.RunStep(t, p.Ctx, worker, rec, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		pvcSuppressed(t, p, b, c)
		pvRun(t, p, b, reader, fixture.Exec("BEGIN", 0))
		pvRun(t, p, b, reader, fixture.Exec("LOCK TABLE pv_part.heap IN ACCESS EXCLUSIVE MODE", 0))
		notices = nil
		fixture.RunStep(t, p.Ctx, worker, rec, fixture.Exec("VACUUM (SKIP_LOCKED) pv_part.heap", 0))
		want = fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "55P03", Message: "skipping vacuum of \"heap\" --- lock not available"}
		if len(notices) != 1 || notices[0] != want {
			t.Fatal("skipped VACUUM notices", notices, want)
		}
		pvRun(t, p, b, reader, fixture.Exec("ROLLBACK", 0))
		pvcSuppressed(t, p, b, c)
		waiterPID, holderPID := 0, 0
		if b.PG {
			waiterPID = fixture.LockPID(t, p.Ctx, c)
			holderPID = fixture.LockPID(t, p.Ctx, reader)
		}
		oid := pvOID(t, p, c, "pv_part.heap")
		pvRun(t, p, b, reader, fixture.Exec("BEGIN", 0))
		pvRun(t, p, b, reader, fixture.Exec("LOCK TABLE pv_part.heap IN SHARE MODE", 0))
		fixture.BinaryConversionActiveCancelAfter(t, p, b, c, "VACUUM pv_part.heap", "(*relationRequest).wait", func(ctx context.Context) {
			if b.PG {
				fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, "ShareUpdateExclusiveLock")
			}
		}, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := c.QueryContext(ctx, "VACUUM pv_part.heap")
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		pvRun(t, p, b, reader, fixture.Exec("ROLLBACK", 0))
		pvcSuppressed(t, p, b, c)
		pvRun(t, p, b, c, fixture.Exec("VACUUM pv_part.heap", 0))
		pvOneFrozen(t, p, b, c)
	})
}

func TestReferencePgvisibilityCheckCancellation(t *testing.T) {
	for _, fn := range pvcFunctions {
		t.Run(fn, func(t *testing.T) {
			pvcCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				pvRun(t, p, b, c, fixture.Exec("INSERT INTO pv_part.heap VALUES(1)", 1))
				pvFreeze(t, p, b, c)
				oid := pvOID(t, p, c, "pv_part.heap")
				holder := pvConn(t, p, b)
				defer holder.Close()
				defer pvRollback(holder)
				q := "SELECT * FROM pv_part." + fn + "($1::oid)"
				st, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				waiterPID, holderPID := 0, 0
				if b.PG {
					waiterPID = fixture.LockPID(t, p.Ctx, c)
					holderPID = fixture.LockPID(t, p.Ctx, holder)
				}
				pvRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
				heldMode, waitMode := "ACCESS EXCLUSIVE", "AccessShareLock"
				if fn == pvcFunctions[2] {
					heldMode = "ACCESS SHARE"
					waitMode = "AccessExclusiveLock"
				}
				pvRun(t, p, b, holder, fixture.Exec("LOCK TABLE pv_part.heap IN "+heldMode+" MODE", 0))
				fixture.BinaryConversionActiveCancelAfter(t, p, b, c, q, "(*relationRequest).wait", func(ctx context.Context) {
					if b.PG {
						fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, waitMode)
					}
				}, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := st.QueryContext(ctx, oid)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				pvRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
				pvOneFrozen(t, p, b, c)
				pvcChecks(t, p, b, c, "pv_part.heap", nil)
				pvRun(t, p, b, c, fixture.Exec("BEGIN", 0))
				defer pvRollback(c)
				pvcTruncate(t, p, b, c, "pv_part.heap")
				// A completed call releases its scoped AX even while caller is in BEGIN.
				pvRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
				pvRun(t, p, b, holder, fixture.Exec("LOCK TABLE pv_part.heap IN ACCESS SHARE MODE NOWAIT", 0))
				pvRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
				pvRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
				pvcSuppressed(t, p, b, c)
			})
		})
	}
}
