package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMaterializedStorageCancellation(t *testing.T) {
	for _, suffix := range []string{"ALTER note SET STORAGE EXTERNAL, ALTER id SET STATISTICS 7, SET (fillfactor=70), CLUSTER ON ms_a", "SET ACCESS METHOD ms_heap2", "SET TABLESPACE pg_default"} {
		t.Run(suffix, func(t *testing.T) {
			materializedStorageRoutesMode(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				materializedStorageSeed(run)
				run(fixture.Exec("CREATE ACCESS METHOD ms_heap2 TYPE TABLE HANDLER heap_tableam_handler", 0))
				holder, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Close()
				observer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer observer.Close()
				q := "ALTER MATERIALIZED VIEW ms_mv " + suffix
				var held *sql.Stmt
				if prepared {
					held, err = c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				workerPID, holderPID := 0, 0
				var oid int64
				if b.PG {
					workerPID = fixture.LockPID(t, p.Ctx, c)
					holderPID = fixture.LockPID(t, p.Ctx, holder)
					oid = fixture.LockOID(t, p.Ctx, "ms_mv")
				}
				tx, err := holder.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				// LOCK TABLE rejects materialized views on PostgreSQL; SELECT is
				// the actual AccessShare holder and remains open through its Tx.
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, materializedStorageRows())
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				finished := make(chan struct{})
				join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					var result sql.Result
					var err error
					if prepared {
						result, err = held.ExecContext(ctx)
					} else {
						result, err = c.ExecContext(ctx, q)
					}
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				fixture.RelationLockPending(t, p, b, workerPID, holderPID, oid, "AccessExclusiveLock", finished)
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, materializedStorageColumns(nil, nil, "x", ""))
				if b.PG {
					var ok bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&ok); err != nil || !ok {
						t.Fatal(ok, err)
					}
				} else {
					cancel()
				}
				out := join()
				if out.Result != nil || out.Rows != nil {
					t.Fatal("canceled ALTER exposed output")
				}
				if b.PG {
					want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
					fixture.AssertDiagnostic(t, out.Err, &want)
				} else if !errors.Is(out.Err, context.Canceled) {
					t.Fatal(out.Err)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				materializedStorageBaseline(run)
				run(fixture.Select("SELECT count(*) FROM pg_locks WHERE NOT granted", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				// The held route reuses the exact canceled handle.
				var executor fixture.Executor = c
				if prepared {
					executor = fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
				}
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 0))
				run(materializedStorageRows())
			})
		})
	}
}

func TestReferenceMaterializedStorageLockModes(t *testing.T) {
	for _, tc := range []struct{ name, holder, action, mode string }{
		{"statistics_sue", "ALTER MATERIALIZED VIEW ms_mv ALTER id SET STATISTICS 3", "ALTER id SET STATISTICS 7", "ShareUpdateExclusiveLock"},
		{"options_sue", "ALTER MATERIALIZED VIEW ms_mv SET (fillfactor=70)", "SET (fillfactor=80)", "ShareUpdateExclusiveLock"},
		{"cluster_sue", "ALTER MATERIALIZED VIEW ms_mv CLUSTER ON ms_b", "CLUSTER ON ms_a", "ShareUpdateExclusiveLock"},
		{"compression_ax", "SELECT id,note FROM ms_mv", "ALTER note SET COMPRESSION pglz", "AccessExclusiveLock"},
		{"user_catalog_ax", "SELECT id,note FROM ms_mv", "SET (user_catalog_table=true)", "AccessExclusiveLock"},
		{"mixed_strongest", "SELECT id,note FROM ms_mv", "ALTER id SET STATISTICS 7, ALTER note SET STORAGE EXTERNAL", "AccessExclusiveLock"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			materializedStorageRoutesMode(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				materializedStorageSeed(run)
				holder, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Close()
				q := "ALTER MATERIALIZED VIEW ms_mv " + tc.action
				var held *sql.Stmt
				if prepared {
					held, err = c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				workerPID, holderPID := 0, 0
				var oid int64
				if b.PG {
					workerPID = fixture.LockPID(t, p.Ctx, c)
					holderPID = fixture.LockPID(t, p.Ctx, holder)
					oid = fixture.LockOID(t, p.Ctx, "ms_mv")
				}
				tx, err := holder.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if tc.holder == "SELECT id,note FROM ms_mv" {
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, materializedStorageRows())
				} else {
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(tc.holder, 0))
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				finished := make(chan struct{})
				join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					var result sql.Result
					var err error
					if prepared {
						result, err = held.ExecContext(ctx)
					} else {
						result, err = c.ExecContext(ctx, q)
					}
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				fixture.RelationLockPending(t, p, b, workerPID, holderPID, oid, tc.mode, finished)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				out := join()
				if out.Err != nil || out.Result == nil {
					t.Fatal(out.Err)
				}
				if n, err := out.Result.RowsAffected(); err != nil || n != 0 {
					t.Fatal(n, err)
				}
				run(materializedStorageRows())
			})
		})
	}
	// SUE metadata does not block an already held reader's AccessShare.
	t.Run("reader_compatible", func(t *testing.T) {
		materializedStorageRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
			materializedStorageSeed(run)
			holder, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Close()
			tx, err := holder.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, materializedStorageRows())
			run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv ALTER id SET STATISTICS 7, SET (fillfactor=70), CLUSTER ON ms_a", 0))
			run(materializedStorageColumns(int64(7), nil, "x", ""))
			run(materializedStorageRows())
		})
	})
}
