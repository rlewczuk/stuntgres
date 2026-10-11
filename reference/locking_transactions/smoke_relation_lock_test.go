package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSmokeRelationLockSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := p.Ctx
			a, b := fixture.LockConnections(t, ctx, backend.DB)
			affected := func(ctx context.Context, x fixture.Executor, q string, prepared bool, want int64) error {
				fixture.RunStep(t, ctx, x, backend.Recorder, fixture.Step{Query: q, Prepared: prepared, Affected: want})
				return nil
			}
			values := func(ctx context.Context, x fixture.Executor, q string, prepared bool, want ...int64) error {
				fixture.RunStep(t, ctx, x, backend.Recorder, fixture.TransactionRows(prepared, q, want...))
				return nil
			}
			var err error
			for _, prepared := range []bool{false, true} {
				for _, commit := range []bool{false, true} {
					if _, err = a.ExecContext(ctx, "DROP TABLE IF EXISTS t"); err != nil {
						t.Fatal(err)
					}
					if err = affected(ctx, a, "CREATE TABLE t(v INT)", false, 0); err != nil {
						t.Fatal(err)
					}
					tx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					if err = affected(ctx, tx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", prepared, 0); err != nil {
						tx.Rollback()
						t.Fatal(err)
					}
					if err = affected(ctx, tx, "INSERT INTO t VALUES(1)", prepared, 1); err != nil {
						tx.Rollback()
						t.Fatal(err)
					}
					if !backend.PG {
						for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(2)"} {
							bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
							o := fixture.RowExecute(bounded, b, q, prepared)
							err = o.Err
							if o.Result != nil {
								t.Fatal("failed wait returned result")
							}
							cancel()
							if !errors.Is(err, context.DeadlineExceeded) {
								tx.Rollback()
								t.Fatal("held AX cancellation", q, err)
							}
							var diagnostic interface{ SQLState() string }
							if errors.As(err, &diagnostic) {
								tx.Rollback()
								t.Fatal("fabricated context SQLSTATE", diagnostic)
							}
						}
					} else {
						affected(ctx, b, "SET statement_timeout=30", false, 0)
						for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(2)"} {
							o := fixture.RowExecute(ctx, b, q, prepared)
							if o.Result != nil {
								t.Fatal("failed wait returned result")
							}
							fixture.WaitsDiagnostic(t, o.Err, "57014", "canceling statement due to statement timeout")
						}
						affected(ctx, b, "SET statement_timeout=0", false, 0)
					}
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					if commit {
						err = values(ctx, b, "SELECT v FROM t ORDER BY v", prepared, 1)
					} else {
						err = values(ctx, b, "SELECT v FROM t ORDER BY v", prepared)
					}
					if err != nil {
						t.Fatal(err)
					}
					if err = affected(ctx, b, "INSERT INTO t VALUES(2)", prepared, 1); err != nil {
						t.Fatal(err)
					}
					if commit {
						err = values(ctx, b, "SELECT v FROM t ORDER BY v", prepared, 1, 2)
					} else {
						err = values(ctx, b, "SELECT v FROM t ORDER BY v", prepared, 2)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(3)"} {
				stmt, err := a.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				stmt.Close()
				writer, err := b.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err = affected(ctx, writer, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0); err != nil {
					writer.Rollback()
					t.Fatal("implicit prepare leaked", err)
				}
				writer.Rollback()
				reader, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				stmt, err = reader.PrepareContext(ctx, q)
				if err != nil {
					reader.Rollback()
					t.Fatal(err)
				}
				stmt.Close()
				if err = affected(ctx, b, "INSERT INTO t VALUES(7)", false, 1); err != nil {
					reader.Rollback()
					t.Fatal("AS/RX compatibility", err)
				}
				writer, err = b.BeginTx(ctx, nil)
				if err != nil {
					reader.Rollback()
					t.Fatal(err)
				}
				bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
				if backend.PG {
					affected(ctx, writer, "SET LOCAL statement_timeout=30", false, 0)
					_, err = writer.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
				} else {
					_, err = writer.ExecContext(bounded, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
				}
				cancel()
				writer.Rollback()
				reader.Rollback()
				if backend.PG {
					fixture.WaitsDiagnostic(t, err, "57014", "canceling statement due to statement timeout")
				} else if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("explicit preparation ownership", err)
				}
			}
			check := func(err error, code, message, position string) {
				t.Helper()
				fixture.WaitsDiagnostic(t, err, code, message, position)
			}
			for _, prepared := range []bool{false, true} {
				err = fixture.RowExecute(ctx, a, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", prepared).Err
				check(err, "25P01", "LOCK TABLE can only be used in transaction blocks", "")
			}
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			_, err = tx.ExecContext(ctx, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE")
			check(err, "42P01", `relation "absent" does not exist`, "")
			_, err = tx.ExecContext(ctx, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE")
			check(err, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
			_, err = tx.ExecContext(ctx, "SELECT * FROM t WHERE v = )")
			check(err, "42601", `syntax error at or near ")"`, "27")
			tx.Rollback()
			affected(ctx, b, "INSERT INTO t VALUES(8)", false, 1)
		})
	}
}
