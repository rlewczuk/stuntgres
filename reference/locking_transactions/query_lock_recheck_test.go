package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceQueryLockSelectedRechecks(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			change   string
			rollback bool
			want     []int64
		}{
			{"UPDATE t SET v=30 WHERE id=1", false, []int64{40}},
			{"UPDATE t SET v=30 WHERE id=1", true, []int64{20}},
			{"DELETE FROM t WHERE id=1", false, nil},
			{"UPDATE t SET id=5 WHERE id=1", false, nil},
		} {
			t.Run(fmt.Sprintf("%s/%v/%v", tc.change, tc.rollback, prepared), func(t *testing.T) {
				fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
					fixture.LockExec(t, p.Ctx, a, "CREATE SEQUENCE s", 0)
					ap, bp := 0, 0
					if b.PG {
						ap, bp = fixture.RowPIDs(t, p.Ctx, a, peer)
					}
					holder := fixture.QueryLockBegin(t, p.Ctx, a)
					fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
					fixture.RowAffected(t, p.Ctx, holder, tc.change, false, 1)
					fixture.RowAffected(t, p.Ctx, holder, "UPDATE u SET v=70 WHERE id=1", false, 1)
					fixture.RowAffected(t, p.Ctx, holder, "INSERT INTO t VALUES(5,50)", false, 1)
					// b is deliberately unmarked: its originally captured v=10 must stay frozen.
					// The shared volatile output is also the ORDER key, an actual pre-lock
					// consuming frontier rather than an assumption about target demand timing.
					q := "SELECT a.v+b.v+nextval('s')::int*0 AS v FROM t a JOIN u b ON a.id=b.id WHERE b.id=1 ORDER BY v FOR UPDATE OF a"
					ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
					defer cancel()
					done := fixture.RowAsync(t, ctx, peer, q, prepared, true, b.Recorder.RawBackend)
					if b.PG {
						fixture.RowWait(t, ctx, bp, ap)
					} else {
						fixture.LimitSequenceCapture(t, ctx, holder)
					}
					if tc.rollback {
						if err := holder.Rollback(); err != nil {
							t.Fatal(err)
						}
					} else {
						if err := holder.Commit(); err != nil {
							t.Fatal(err)
						}
					}
					out := fixture.LockResult(t, done)
					fixture.LimitLockValues(t, out, b.PG, tc.want...)
				})
			})
		}
	}
	// A row remains held after its refreshed predicate rejects it; there is no
	// search for a new join combination or newly inserted source row.
	fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		fixture.LockExec(t, p.Ctx, a, "CREATE SEQUENCE s", 0)
		ap, bp := 0, 0
		if b.PG {
			ap, bp = fixture.RowPIDs(t, p.Ctx, a, peer)
		}
		holder := fixture.QueryLockBegin(t, p.Ctx, a)
		fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
		fixture.RowAffected(t, p.Ctx, holder, "UPDATE t SET v=99 WHERE id=1", false, 1)
		reader := fixture.QueryLockBegin(t, p.Ctx, peer)
		q := "SELECT a.v+nextval('s')::int*0 AS v FROM t a JOIN u b USING(id) WHERE a.v=10 ORDER BY v FOR UPDATE OF a"
		done := fixture.RowAsync(t, p.Ctx, reader, q, true, true, b.Recorder.RawBackend)
		if b.PG {
			fixture.RowWait(t, p.Ctx, bp, ap)
		} else {
			fixture.LimitSequenceCapture(t, p.Ctx, holder)
		}
		if err := holder.Commit(); err != nil {
			t.Fatal(err)
		}
		fixture.LimitLockValues(t, fixture.LockResult(t, done), b.PG)
		queryLockMigratedBlocked(t, p.Ctx, a, b.PG, "UPDATE t SET v=v WHERE id=1", false)
		fixture.RowAffected(t, p.Ctx, a, "UPDATE u SET v=v WHERE id=1", false, 1)
		if err := reader.Rollback(); err != nil {
			t.Fatal(err)
		}
		fixture.RowAffected(t, p.Ctx, a, "UPDATE t SET v=v WHERE id=1", false, 1)
	})
	// Stable isolation uses the prerequisite's retained-frontier rules through OF.
	for _, isolation := range []sql.IsolationLevel{sql.LevelRepeatableRead, sql.LevelSerializable} {
		for _, change := range []string{"UPDATE t SET v=30 WHERE id=1", "DELETE FROM t WHERE id=1"} {
			t.Run(fmt.Sprintf("stable/%v/%s", isolation, change), func(t *testing.T) {
				fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
					reader, err := peer.BeginTx(p.Ctx, &sql.TxOptions{Isolation: isolation})
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Rollback()
					fixture.QueryLockRollbackCleanup(t, reader)
					fixture.QueryLockRows(t, p, b, reader, "SELECT v FROM t WHERE id=1", false, "v", 10)
					fixture.RowAffected(t, p.Ctx, a, change, false, 1)
					if change[0] == 'U' {
						fixture.QueryLockRows(t, p, b, reader, "SELECT v FROM t WHERE id=1 FOR KEY SHARE OF t NOWAIT", true, "v", 10)
					} else {
						fixture.QueryLockFailure(t, p, b, reader, "SELECT v FROM t WHERE id=1 FOR KEY SHARE OF t SKIP LOCKED", false, fixture.Diagnostic{Code: "40001", Message: "could not serialize access due to concurrent update"}, "execute")
					}
				})
			})
		}
	}
}
