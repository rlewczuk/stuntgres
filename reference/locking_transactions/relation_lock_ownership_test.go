package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRelationLockOwnership(t *testing.T) {
	p := fixture.NewScenario(t)
	nativeDSN := "reference/" + t.Name()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := p.Ctx
			a, b := fixture.LockConnections(t, ctx, backend.DB)
			fixture.LockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.LockExec(t, ctx, tx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
			if !backend.PG {
				for _, cohort := range []struct {
					d   *stuntgres.Driver
					dsn string
				}{{p.NativeConnector.Driver().(*stuntgres.Driver), t.Name() + "/other"}, {stuntgres.NewDriver(), nativeDSN}} {
					connector, err := cohort.d.OpenConnector(cohort.dsn)
					if err != nil {
						t.Fatal(err)
					}
					other := sql.OpenDB(connector)
					t.Cleanup(func() { other.Close() })
					fixture.LockExec(t, ctx, other, "CREATE TABLE t(v INT)", 0)
					bounded, cancel := context.WithTimeout(ctx, time.Second)
					fixture.LockExec(t, bounded, other, "INSERT INTO t VALUES(7)", 1)
					rows, err := other.QueryContext(bounded, "SELECT v FROM t")
					fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, false, 7)
					cancel()
				}
				bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
				r, err := b.ExecContext(bounded, "INSERT INTO t VALUES(2)")
				cancel()
				if r != nil || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("same Driver/DSN did not arbitrate", r, err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("caller deadline acquired SQLSTATE", err)
				}
			}
			if backend.PG {
				ap, bp := fixture.RowPIDs(t, ctx, a, b)
				await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					result, err := b.ExecContext(ctx, "INSERT INTO t VALUES(2)")
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				fixture.LockWaitBehind(t, ctx, bp, ap, fixture.LockOID(t, ctx, "t"), "RowExclusiveLock")
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				out := await()
				if out.Err != nil {
					t.Fatal(out.Err)
				}
				fixture.LockAffected(t, out.Result, 1)
			} else {
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, ctx, b, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(2)", 1))
			}
			fixture.RunStep(t, ctx, b, backend.Recorder, fixture.TransactionRows(false, "SELECT v FROM t", 2))
		})
	}
}

func referenceRelationRowsCloseHold(t *testing.T, p *fixture.Pair, backend fixture.Backend, a, b *sql.Conn) {
	t.Helper()
	ctx := p.Ctx
	reader, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Rollback() })
	rows, err := reader.QueryContext(ctx, "SELECT v FROM t")
	fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, backend.PG)
	writer, err := b.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Rollback() })
	if !backend.PG {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		r, err := writer.ExecContext(bounded, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		cancel()
		if r != nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("Rows.Close unlocked explicit AS", r, err)
		}
		if err := writer.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err := reader.Rollback(); err != nil {
			t.Fatal(err)
		}
	} else {
		ap, bp := fixture.RowPIDs(t, ctx, a, b)
		await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
			r, err := writer.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			return fixture.RelationOutcome{Result: r, Err: err}
		})
		fixture.LockWaitBehind(t, ctx, bp, ap, fixture.LockOID(t, ctx, "t"), "AccessExclusiveLock")
		if err := reader.Rollback(); err != nil {
			t.Fatal(err)
		}
		o := await()
		if o.Err != nil {
			t.Fatal(o.Err)
		}
		fixture.LockAffected(t, o.Result, 0)
		if err := writer.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}
