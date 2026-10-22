package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSimilarActualCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					fixture.StringCoreControlExec(t, b.DB, `CREATE TABLE t(v TEXT,p TEXT,sentinel INT)`)
					// Keep the original regex workload; the held relation makes admission durable.
					input := strings.Repeat("a", 8*1024*1024)
					result, err := b.DB.ExecContext(p.Ctx, `INSERT INTO t VALUES($1,'(a|aa)*',7)`, input)
					if err != nil {
						t.Fatal(err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal(n, err)
					}
					conn, blocker := fixture.LockConnections(t, p.Ctx, b.DB)
					identity := fixture.SettingsPhysical(t, conn)
					run := func(x fixture.Executor, s fixture.Step) {
						fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
					}
					const query = `UPDATE t SET sentinel=$1 WHERE v SIMILAR TO p`
					var held *sql.Stmt
					if prepared {
						held, err = conn.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
					}
					exec := func(ctx context.Context, value int) (sql.Result, error) {
						if held != nil {
							return held.ExecContext(ctx, value)
						}
						return conn.ExecContext(ctx, query, value)
					}
					run(blocker, fixture.Exec("BEGIN", 0))
					released := false
					t.Cleanup(func() {
						if !released {
							ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
							defer cancel()
							_, _ = blocker.ExecContext(ctx, "ROLLBACK")
						}
					})
					run(blocker, fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0))
					fixture.RelationLockActiveCancel(t, p, b, conn, query, func(ctx context.Context) fixture.RelationOutcome {
						r, err := exec(ctx, 99)
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					run(blocker, fixture.Exec("ROLLBACK", 0))
					released = true
					check := func(value int64) {
						run(conn, fixture.Select("SELECT sentinel FROM t", []string{"sentinel"}, []string{"INT4"}, [][]any{{value}}))
						if fixture.SettingsPhysical(t, conn) != identity {
							t.Fatal("SIMILAR recovery lost physical owner")
						}
					}
					check(7)
					run(conn, fixture.Exec("UPDATE t SET v='a',p='a'", 1))
					result, err = exec(p.Ctx, 8)
					if err != nil || result == nil {
						t.Fatal("same-consumer recovery", result, err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal("same-consumer recovery affected count", n, err)
					}
					check(8)
				})
			}
		})
	}
}
