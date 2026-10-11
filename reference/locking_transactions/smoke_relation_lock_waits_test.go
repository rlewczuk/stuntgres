package reference_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceSmokeRelationLockWaitsSmokeNowait(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			a, b := fixture.LockConnections(t, ctx, backend.DB)
			fixture.RunStep(t, ctx, a, backend.Recorder, fixture.Exec("CREATE TABLE t(v INT)", 0))
			affected := func(ctx context.Context, x fixture.Executor, q string, prepared bool, want int64) error {
				fixture.RunStep(t, ctx, x, backend.Recorder, fixture.Step{Query: q, Prepared: prepared, Affected: want})
				return nil
			}
			values := func(ctx context.Context, x fixture.Executor, q string, prepared bool, want ...int64) error {
				fixture.RunStep(t, ctx, x, backend.Recorder, fixture.TransactionRows(prepared, q, want...))
				return nil
			}
			if _, err := a.ExecContext(ctx, `CREATE TABLE "T"(v INT)`); err != nil {
				t.Fatal(err)
			}
			holder, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			if err = affected(ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0); err != nil {
				t.Fatal(err)
			}
			if err = affected(ctx, holder, "INSERT INTO t VALUES(1)", false, 1); err != nil {
				t.Fatal(err)
			}
			if err = affected(ctx, holder, `LOCK TABLE "T" IN ACCESS EXCLUSIVE MODE`, false, 0); err != nil {
				t.Fatal(err)
			}
			for _, prepared := range []bool{false, true} {
				for _, name := range []string{"t", "public.t", `"T"`, `public."T"`} {
					tx, err := b.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					query := "LOCK TABLE " + name + " IN ACCESS EXCLUSIVE MODE NOWAIT"
					if prepared {
						stmt, prepareErr := tx.PrepareContext(ctx, query)
						if prepareErr != nil {
							tx.Rollback()
							t.Fatalf("NOWAIT Prepare must be metadata-only: %v", prepareErr)
						}
						_, err = stmt.ExecContext(ctx)
						stmt.Close()
					} else {
						_, err = tx.ExecContext(ctx, query)
					}
					tx.Rollback()
					decoded := name
					if name == `"T"` {
						decoded = "T"
					}
					if name == `public."T"` {
						decoded = "public.T"
					}
					fixture.WaitsDiagnostic(t, err, "55P03", `could not obtain lock on relation "`+decoded+`"`)
				}
			}
			if err = holder.Commit(); err != nil {
				t.Fatal(err)
			}
			tx, err := b.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for range 2 {
				if err = affected(ctx, tx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", true, 0); err != nil {
					t.Fatal("free/self NOWAIT", err)
				}
			}
			if err = values(ctx, tx, "SELECT v FROM t", false, 1); err != nil {
				t.Fatal(err)
			}
			t.Log("free and retained NOWAIT affected 0; holder commit published row 1")
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			_, err = b.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT")
			fixture.WaitsDiagnostic(t, err, "25P01", "LOCK TABLE can only be used in transaction blocks")
			for _, prepared := range []bool{false, true} {
				if err = affected(ctx, b, "INSERT INTO t VALUES(2)", prepared, 1); err != nil {
					t.Fatal(err)
				}
			}
			rolledBack, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer rolledBack.Rollback()
			if err = affected(ctx, rolledBack, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0); err != nil {
				t.Fatal(err)
			}
			if err = affected(ctx, rolledBack, "INSERT INTO t VALUES(9)", false, 1); err != nil {
				t.Fatal(err)
			}
			if err = rolledBack.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err = values(ctx, b, "SELECT v FROM t ORDER BY v", true, 1, 2, 2); err != nil {
				t.Fatal(err)
			}
			t.Log("outside-block 25P01; direct/cached INSERT affected 1; rollback excluded private 9; final [1 2 2]")
		})
	}
}

func TestReferenceSmokeRelationLockWaitsSmokeTimeoutCancel(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			a, b := fixture.LockConnections(t, ctx, backend.DB)
			fixture.RunStep(t, ctx, a, backend.Recorder, fixture.Exec("CREATE TABLE t(v INT)", 0))
			affected := func(ctx context.Context, x fixture.Executor, q string, prepared bool, want int64) error {
				fixture.RunStep(t, ctx, x, backend.Recorder, fixture.Step{Query: q, Prepared: prepared, Affected: want})
				return nil
			}
			values := func(ctx context.Context, x fixture.Executor, q string, prepared bool, want ...int64) error {
				fixture.RunStep(t, ctx, x, backend.Recorder, fixture.TransactionRows(prepared, q, want...))
				return nil
			}
			var initial string
			if err := b.QueryRowContext(ctx, "SHOW lock_timeout").Scan(&initial); err != nil {
				t.Fatal(err)
			}
			if initial != "0" {
				t.Fatalf("initial lock_timeout = %q, want 0", initial)
			}
			if _, err := b.ExecContext(ctx, "SET lock_timeout = '40ms'"); err != nil {
				t.Fatal(err)
			}
			cached, err := b.PrepareContext(ctx, "SELECT v FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer cached.Close()
			holder, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			if err = affected(ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0); err != nil {
				t.Fatal(err)
			}
			if err = affected(ctx, holder, "INSERT INTO t VALUES(1)", false, 1); err != nil {
				t.Fatal(err)
			}
			_, err = b.ExecContext(ctx, "SELECT v FROM t")
			fixture.WaitsDiagnostic(t, err, "55P03", "canceling statement due to lock timeout", "15")
			rows, err := cached.QueryContext(ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("failed cached query returned a cursor")
			}
			fixture.WaitsDiagnostic(t, err, "55P03", "canceling statement due to lock timeout")
			stmt, err := b.PrepareContext(ctx, "SELECT v FROM t")
			if stmt != nil {
				stmt.Close()
				t.Fatal("failed Prepare returned a handle")
			}
			fixture.WaitsDiagnostic(t, err, "55P03", "canceling statement due to lock timeout", "15")
			tx, err := b.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			_, err = tx.ExecContext(ctx, "SELECT v FROM t")
			fixture.WaitsDiagnostic(t, err, "55P03", "canceling statement due to lock timeout", "15")
			_, err = tx.ExecContext(ctx, "SELECT v FROM t")
			tx.Rollback()
			fixture.WaitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
			if _, err = b.ExecContext(ctx, "SET lock_timeout = 0"); err != nil {
				t.Fatal(err)
			}
			for _, lock := range []string{"1s", "40ms"} {
				if _, err = b.ExecContext(ctx, "SET lock_timeout='"+lock+"'"); err != nil {
					t.Fatal(err)
				}
				if _, err = b.ExecContext(ctx, "SET statement_timeout='40ms'"); err != nil {
					t.Fatal(err)
				}
				_, err = b.ExecContext(ctx, "SELECT v FROM t")
				fixture.WaitsDiagnostic(t, err, "57014", "canceling statement due to statement timeout")
				if _, err = b.ExecContext(ctx, "SET statement_timeout=0"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = b.ExecContext(ctx, "SET lock_timeout=0"); err != nil {
				t.Fatal(err)
			}
			bounded, stop := context.WithTimeout(ctx, 40*time.Millisecond)
			if backend.PG {
				affected(ctx, b, "SET statement_timeout=40", false, 0)
				_, err = b.ExecContext(ctx, "SELECT v FROM t")
			} else {
				_, err = b.ExecContext(bounded, "SELECT v FROM t")
			}
			stop()
			var diagnostic interface{ SQLState() string }
			if !backend.PG && (!errors.Is(err, context.DeadlineExceeded) || errors.As(err, &diagnostic)) {
				t.Fatalf("caller deadline must retain context identity without SQLSTATE: %v", err)
			}
			if backend.PG {
				fixture.WaitsDiagnostic(t, err, "57014", "canceling statement due to statement timeout")
				affected(ctx, b, "SET statement_timeout=0", false, 0)
			}
			if err = holder.Commit(); err != nil {
				t.Fatal(err)
			}
			if err = values(ctx, b, "SELECT v FROM t", true, 1); err != nil {
				t.Fatal(err)
			}
			if err = affected(ctx, b, "INSERT INTO t VALUES(2)", true, 1); err != nil {
				t.Fatal(err)
			}
			if err = values(ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 2); err != nil {
				t.Fatal(err)
			}
			t.Log("lock_timeout default 0; direct and semantic Prepare timeout; explicit abort recovered; caller deadline retained; holder unaffected; healthy final rows [1 2]")

		})
	}
}
