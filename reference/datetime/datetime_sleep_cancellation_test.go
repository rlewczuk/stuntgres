package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func datetimeSleepActiveCancellation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET statement_timeout=0", 0))
	peer, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	for _, expr := range []string{"pg_sleep(600)", "pg_sleep_for(interval '10 minutes')", "pg_sleep_until(clock_timestamp()+interval '10 minutes')", "pg_sleep('Infinity'::float8)", "pg_sleep('1e300'::float8)"} {
		for _, prepared := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				t.Run(fmt.Sprintf("active/%s/prepared=%t/explicit=%t", expr, prepared, explicit), func(t *testing.T) {
					if explicit {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
						defer c.ExecContext(p.Ctx, "ROLLBACK")
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sleep_cancel_probe VALUES(99)", 1))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("LOCK TABLE sleep_cancel_probe IN ACCESS EXCLUSIVE MODE", 0))
					}
					q := "SELECT " + expr
					var stmt *sql.Stmt
					if prepared {
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					fixture.SleepActiveCancel(t, p, b, c, q, func(ctx context.Context) {
						var healthy int
						if err := peer.QueryRowContext(ctx, "SELECT 1").Scan(&healthy); err != nil || healthy != 1 {
							t.Fatalf("independent peer during active wait: %d %v", healthy, err)
						}
					}, func(ctx context.Context) fixture.RelationOutcome {
						if prepared {
							rows, err := stmt.QueryContext(ctx)
							return fixture.RelationOutcome{Rows: rows, Err: err}
						}
						rows, err := c.QueryContext(ctx, q)
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					if explicit {
						datetimeSleepFailure(t, p, b, c, "SELECT 1", datetimeSleepError("25P02", "current transaction is aborted, commands ignored until end of transaction block"), false, false)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
						// Grant after rollback proves execution/table owner cleanup,
						// without inventing shared pre-rollback timing semantics.
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("BEGIN", 0))
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("LOCK TABLE sleep_cancel_probe IN ACCESS EXCLUSIVE MODE", 0))
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("ROLLBACK", 0))
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT 1 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(1)}}))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) AS n FROM sleep_cancel_probe", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				})
			}
		}
	}
}

// VALUES order makes the first row's zero-delay expression complete before
// the second row enters its observed wait. Neither row may publish on error.
func datetimeSleepAtomicWriteCancellation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET statement_timeout=0", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE sleep_cancel_write(id integer,v text)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sleep_cancel_write VALUES(77,'retained')", 1))
	peer, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("atomic-write/prepared=%t", prepared), func(t *testing.T) {
			q := "INSERT INTO sleep_cancel_write VALUES(1,pg_sleep(0)::text),(2,pg_sleep(600)::text)"
			var stmt *sql.Stmt
			if prepared {
				var err error
				stmt, err = c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
			}
			fixture.SleepActiveCancel(t, p, b, c, q, func(ctx context.Context) {
				fixture.RunStep(t, ctx, peer, b.Recorder, fixture.Select("SELECT count(*) AS n FROM sleep_cancel_write", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			}, func(ctx context.Context) fixture.RelationOutcome {
				if prepared {
					result, err := stmt.ExecContext(ctx)
					return fixture.RelationOutcome{Result: result, Err: err}
				}
				result, err := c.ExecContext(ctx, q)
				return fixture.RelationOutcome{Result: result, Err: err}
			})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,v FROM sleep_cancel_write ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(77), "retained"}}))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT id,v FROM sleep_cancel_write ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(77), "retained"}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sleep_cancel_write VALUES(3,'healthy')", 1))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT id,v FROM sleep_cancel_write ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "healthy"}, {int64(77), "retained"}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM sleep_cancel_write WHERE id=3", 1))
		})
	}
}
