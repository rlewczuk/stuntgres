package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres"
)

func TestReferenceRelationLockOwnership(t *testing.T) {
	p := newReferenceScenario(t)
	nativeDSN := "reference/" + t.Name()
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := p.ctx
			a, b := referenceLockConnections(t, ctx, backend.db)
			referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceLockExec(t, ctx, tx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
			if !backend.pg {
				for _, cohort := range []struct {
					d   *stuntgres.Driver
					dsn string
				}{{p.nativeConnector.Driver().(*stuntgres.Driver), t.Name() + "/other"}, {stuntgres.NewDriver(), nativeDSN}} {
					connector, err := cohort.d.OpenConnector(cohort.dsn)
					if err != nil {
						t.Fatal(err)
					}
					other := sql.OpenDB(connector)
					t.Cleanup(func() { other.Close() })
					referenceLockExec(t, ctx, other, "CREATE TABLE t(v INT)", 0)
					bounded, cancel := context.WithTimeout(ctx, time.Second)
					referenceLockExec(t, bounded, other, "INSERT INTO t VALUES(7)", 1)
					rows, err := other.QueryContext(bounded, "SELECT v FROM t")
					referenceLimitLockValues(t, relationOutcome{rows: rows, err: err}, false, 7)
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
			if backend.pg {
				ap, bp := referenceRowPIDs(t, ctx, a, b)
				await := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
					result, err := b.ExecContext(ctx, "INSERT INTO t VALUES(2)")
					return relationOutcome{result: result, err: err}
				})
				referenceLockWaitBehind(t, ctx, bp, ap, referenceLockOID(t, ctx, "t"), "RowExclusiveLock")
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				out := await()
				if out.err != nil {
					t.Fatal(out.err)
				}
				referenceLockAffected(t, out.result, 1)
			} else {
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, ctx, b, backend.recorder, referenceExec("INSERT INTO t VALUES(2)", 1))
			}
			referenceRunStep(t, ctx, b, backend.recorder, referenceTransactionRows(false, "SELECT v FROM t", 2))
		})
	}
}

func referenceRelationRowsCloseHold(t *testing.T, p *referencePair, backend referenceBackend, a, b *sql.Conn) {
	t.Helper()
	ctx := p.ctx
	reader, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Rollback() })
	rows, err := reader.QueryContext(ctx, "SELECT v FROM t")
	referenceLimitLockValues(t, relationOutcome{rows: rows, err: err}, backend.pg)
	writer, err := b.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Rollback() })
	if !backend.pg {
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
		ap, bp := referenceRowPIDs(t, ctx, a, b)
		await := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
			r, err := writer.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			return relationOutcome{result: r, err: err}
		})
		referenceLockWaitBehind(t, ctx, bp, ap, referenceLockOID(t, ctx, "t"), "AccessExclusiveLock")
		if err := reader.Rollback(); err != nil {
			t.Fatal(err)
		}
		o := await()
		if o.err != nil {
			t.Fatal(o.err)
		}
		referenceLockAffected(t, o.result, 0)
		if err := writer.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}
