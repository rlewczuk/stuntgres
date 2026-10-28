package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterClusterCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				clusterSetup(t, p, b, c)
				persistenceExec(t, p, b, c, "ALTER TABLE t CLUSTER ON a")
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
				query := "ALTER TABLE t CLUSTER ON b"
				var held *sql.Stmt
				if prepared {
					held, err = c.PrepareContext(p.Ctx, query)
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
					oid = fixture.LockOID(t, p.Ctx, "t")
				}
				tx, err := holder.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				persistenceExec(t, p, b, tx, "LOCK TABLE t IN SHARE MODE")
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
						result, err = c.ExecContext(ctx, query)
					}
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				fixture.RelationLockPending(t, p, b, workerPID, holderPID, oid, "ShareUpdateExclusiveLock", finished)
				clusterCheck(t, p, b, observer, true, false)
				if b.PG {
					var ok bool
					if err = fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&ok); err != nil || !ok {
						t.Fatal(ok, err)
					}
				} else {
					cancel()
				}
				out := join()
				if out.Result != nil || out.Rows != nil {
					t.Fatal("canceled ALTER exposed result")
				}
				if b.PG {
					want := persistenceDiagnostic("57014", "canceling statement due to user request")
					fixture.AssertDiagnostic(t, out.Err, &want)
				} else if !errors.Is(out.Err, context.Canceled) {
					t.Fatal(out.Err)
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				clusterCheck(t, p, b, observer, true, false)
				persistenceExec(t, p, b, observer, query)
				clusterCheck(t, p, b, observer, false, true)
				if !b.PG {
					ctx, stop := context.WithCancel(p.Ctx)
					stop()
					_, err = observer.ExecContext(ctx, "ALTER TABLE t CLUSTER ON a")
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					clusterCheck(t, p, b, observer, false, true)
				}
			})
		})
	}
}

func TestReferenceAlterClusterAuthorizationLocks(t *testing.T) {
	t.Run("owner", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"cluster_owner", "cluster_guest"})
		persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			clusterSetup(t, p, b, c)
			for _, q := range []string{"CREATE ROLE cluster_owner", "CREATE ROLE cluster_guest", "GRANT USAGE ON SCHEMA public TO cluster_owner,cluster_guest", "ALTER TABLE t OWNER TO cluster_owner", "GRANT ALL ON t TO cluster_guest", "ALTER TABLE t CLUSTER ON a"} {
				persistenceExec(t, p, b, c, q)
			}
			if b.PG {
				persistenceExec(t, p, b, c, "GRANT MAINTAIN ON t TO cluster_guest")
			}
			held, err := c.PrepareContext(p.Ctx, "ALTER TABLE t CLUSTER ON b")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			persistenceExec(t, p, b, c, "SET ROLE cluster_guest")
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			want := persistenceDiagnostic("42501", "must be owner of table t")
			_, err = held.ExecContext(p.Ctx)
			fixture.AssertDiagnostic(t, err, &want)
			for _, q := range []string{"CLUSTER ON b", "SET WITHOUT CLUSTER", "SET WITHOUT OIDS"} {
				for _, prepared := range []bool{false, true} {
					persistenceFailure(t, p, b, c, "ALTER TABLE t "+q, prepared, want)
				}
			}
			persistenceExec(t, p, b, c, "RESET ROLE")
			clusterCheck(t, p, b, c, true, false)
			persistenceExec(t, p, b, c, "SET ROLE cluster_owner")
			if _, err = held.ExecContext(p.Ctx); err != nil {
				t.Fatal(err)
			}
			persistenceExec(t, p, b, c, "RESET ROLE")
			clusterCheck(t, p, b, c, false, true)
		})
	})
	for _, v := range []struct {
		name, holder, query, mode string
		compatible                bool
	}{
		{"dml", "ROW EXCLUSIVE", "ALTER TABLE t CLUSTER ON b", "ShareUpdateExclusiveLock", true},
		{"share", "SHARE", "ALTER TABLE t SET WITHOUT CLUSTER", "ShareUpdateExclusiveLock", false},
		{"exclusive", "ACCESS EXCLUSIVE", "ALTER TABLE t CLUSTER ON b", "ShareUpdateExclusiveLock", false},
		{"oids", "ACCESS SHARE", "ALTER TABLE t SET WITHOUT OIDS", "AccessExclusiveLock", false},
		{"companion", "ACCESS SHARE", "ALTER TABLE t CLUSTER ON b, ADD COLUMN extra int", "AccessExclusiveLock", false},
	} {
		t.Run(v.name, func(t *testing.T) {
			persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				clusterSetup(t, p, b, c)
				holder, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Close()
				workerPID, holderPID := 0, 0
				var oid int64
				if b.PG {
					workerPID = fixture.LockPID(t, p.Ctx, c)
					holderPID = fixture.LockPID(t, p.Ctx, holder)
					oid = fixture.LockOID(t, p.Ctx, "t")
				}
				tx, err := holder.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				persistenceExec(t, p, b, tx, "LOCK TABLE t IN "+v.holder+" MODE")
				if v.compatible {
					persistenceExec(t, p, b, c, v.query)
					if err = tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					clusterCheck(t, p, b, c, false, true)
					return
				}
				finished := make(chan struct{})
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					result, err := c.ExecContext(ctx, v.query)
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				fixture.RelationLockPending(t, p, b, workerPID, holderPID, oid, v.mode, finished)
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				out := join()
				if out.Err != nil {
					t.Fatal(out.Err)
				}
			})
		})
	}
}
