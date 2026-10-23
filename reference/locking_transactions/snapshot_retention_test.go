package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotStatementRetention(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "held"
		}
		t.Run(name, func(t *testing.T) {
			snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, bumper, holder, o := snapshotConn(t, p, b), snapshotConn(t, p, b), snapshotConn(t, p, b), snapshotConn(t, p, b)
				const lock = "SELECT pg_advisory_lock(4294967397::bigint)"
				const unlock = "SELECT pg_advisory_unlock(4294967397::bigint)"
				const query = "SELECT CASE WHEN pg_advisory_lock(4294967397::bigint) IS NULL THEN pg_current_snapshot()::text ELSE pg_current_snapshot()::text END AS s"
				var held *sql.Stmt
				if prepared {
					var err error
					held, err = o.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				id := identityID(t, p, a)
				snapshotBump(t, p, bumper, id)
				var ignored any
				if err := holder.QueryRowContext(p.Ctx, lock).Scan(&ignored); err != nil {
					t.Fatal(err)
				}
				// Both advisory owners are explicitly released even if an assertion fails.
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), snapshotCleanupTimeout)
					defer cancel()
					_, _ = holder.ExecContext(ctx, unlock)
					_, _ = o.ExecContext(ctx, unlock)
				})
				ctx, cancel := context.WithTimeout(p.Ctx, 8*time.Second)
				type result struct {
					s   string
					err error
				}
				done := make(chan result, 1)
				joined := false
				go func() {
					var r result
					if held != nil {
						r.err = held.QueryRowContext(ctx).Scan(&r.s)
					} else {
						r.err = o.QueryRowContext(ctx, query).Scan(&r.s)
					}
					done <- r
				}()
				t.Cleanup(func() {
					cancel()
					if !joined {
						select {
						case <-done:
						case <-time.After(10 * time.Second):
							t.Error("snapshot worker failed to join")
						}
					}
				})
				poll, stop := context.WithTimeout(p.Ctx, 5*time.Second)
				defer stop()
				for {
					var pending bool
					err := b.DB.QueryRowContext(poll, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND classid::bigint=1 AND objid::bigint=101 AND objsubid=1 AND NOT granted)").Scan(&pending)
					if err != nil {
						t.Fatal("snapshot lock admission", err)
					}
					if pending {
						break
					}
					runtime.Gosched()
				}
				fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
				var released bool
				if err := holder.QueryRowContext(p.Ctx, unlock).Scan(&released); err != nil || !released {
					t.Fatalf("release holder %v: %v", released, err)
				}
				var r result
				select {
				case r = <-done:
					joined = true
				case <-ctx.Done():
					t.Fatal("snapshot worker", ctx.Err())
				}
				cancel()
				if r.err != nil {
					t.Fatal(r.err)
				}
				if err := o.QueryRowContext(p.Ctx, unlock).Scan(&released); err != nil || !released {
					t.Fatalf("release observer %v: %v", released, err)
				}
				snapshotPeerPresent(t, r.s, id, true)
				snapshotVisible(t, p, b, o, id, r.s, false)
				fresh := snapshotRead(t, p, o, nil)
				snapshotPeerPresent(t, fresh, id, false)
				snapshotVisible(t, p, b, o, id, fresh, true)
			})
		})
	}
}
