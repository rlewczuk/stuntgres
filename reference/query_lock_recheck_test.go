package reference_test

import (
	"context"
	"database/sql"
	"fmt"
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
				queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
					referenceLockExec(t, p.ctx, a, "CREATE SEQUENCE s", 0)
					ap, bp := 0, 0
					if b.pg {
						ap, bp = referenceRowPIDs(t, p.ctx, a, peer)
					}
					holder := queryLockBegin(t, p.ctx, a)
					queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
					referenceRowAffected(t, p.ctx, holder, tc.change, false, 1)
					referenceRowAffected(t, p.ctx, holder, "UPDATE u SET v=70 WHERE id=1", false, 1)
					referenceRowAffected(t, p.ctx, holder, "INSERT INTO t VALUES(5,50)", false, 1)
					// b is deliberately unmarked: its originally captured v=10 must stay frozen.
					// The shared volatile output is also the ORDER key, an actual pre-lock
					// consuming frontier rather than an assumption about target demand timing.
					q := "SELECT a.v+b.v+nextval('s')::int*0 AS v FROM t a JOIN u b ON a.id=b.id WHERE b.id=1 ORDER BY v FOR UPDATE OF a"
					ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
					defer cancel()
					done := referenceRowAsync(t, ctx, peer, q, prepared, true, b.recorder.rawBackend)
					if b.pg {
						referenceRowWait(t, ctx, bp, ap)
					} else {
						referenceLimitSequenceCapture(t, ctx, holder)
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
					out := referenceLockResult(t, done)
					referenceLimitLockValues(t, out, b.pg, tc.want...)
				})
			})
		}
	}
	// A row remains held after its refreshed predicate rejects it; there is no
	// search for a new join combination or newly inserted source row.
	queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
		referenceLockExec(t, p.ctx, a, "CREATE SEQUENCE s", 0)
		ap, bp := 0, 0
		if b.pg {
			ap, bp = referenceRowPIDs(t, p.ctx, a, peer)
		}
		holder := queryLockBegin(t, p.ctx, a)
		queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
		referenceRowAffected(t, p.ctx, holder, "UPDATE t SET v=99 WHERE id=1", false, 1)
		reader := queryLockBegin(t, p.ctx, peer)
		q := "SELECT a.v+nextval('s')::int*0 AS v FROM t a JOIN u b USING(id) WHERE a.v=10 ORDER BY v FOR UPDATE OF a"
		done := referenceRowAsync(t, p.ctx, reader, q, true, true, b.recorder.rawBackend)
		if b.pg {
			referenceRowWait(t, p.ctx, bp, ap)
		} else {
			referenceLimitSequenceCapture(t, p.ctx, holder)
		}
		if err := holder.Commit(); err != nil {
			t.Fatal(err)
		}
		referenceLimitLockValues(t, referenceLockResult(t, done), b.pg)
		queryLockMigratedBlocked(t, p.ctx, a, b.pg, "UPDATE t SET v=v WHERE id=1", false)
		referenceRowAffected(t, p.ctx, a, "UPDATE u SET v=v WHERE id=1", false, 1)
		if err := reader.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRowAffected(t, p.ctx, a, "UPDATE t SET v=v WHERE id=1", false, 1)
	})
	// Stable isolation uses the prerequisite's retained-frontier rules through OF.
	for _, isolation := range []sql.IsolationLevel{sql.LevelRepeatableRead, sql.LevelSerializable} {
		for _, change := range []string{"UPDATE t SET v=30 WHERE id=1", "DELETE FROM t WHERE id=1"} {
			t.Run(fmt.Sprintf("stable/%v/%s", isolation, change), func(t *testing.T) {
				queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
					reader, err := peer.BeginTx(p.ctx, &sql.TxOptions{Isolation: isolation})
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Rollback()
					queryLockRollbackCleanup(t, reader)
					queryLockRows(t, p, b, reader, "SELECT v FROM t WHERE id=1", false, "v", 10)
					referenceRowAffected(t, p.ctx, a, change, false, 1)
					if change[0] == 'U' {
						queryLockRows(t, p, b, reader, "SELECT v FROM t WHERE id=1 FOR KEY SHARE OF t NOWAIT", true, "v", 10)
					} else {
						queryLockFailure(t, p, b, reader, "SELECT v FROM t WHERE id=1 FOR KEY SHARE OF t SKIP LOCKED", false, referenceDiagnostic{Code: "40001", Message: "could not serialize access due to concurrent update"}, "execute")
					}
				})
			})
		}
	}
}
