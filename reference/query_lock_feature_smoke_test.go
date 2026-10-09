package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres"
)

func TestReferenceQueryLockFeatureSmoke(t *testing.T) {
	queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
		holder := queryLockBegin(t, p.ctx, a)
		queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
		for _, prepared := range []bool{false, true} {
			reader := queryLockBegin(t, p.ctx, peer)
			queryLockNowait(t, p, b, reader, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", prepared, "t")
			if err := reader.Rollback(); err != nil {
				t.Fatal(err)
			}
		}
		c, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		for _, prepared := range []bool{false, true} {
			reader := queryLockBegin(t, p.ctx, peer)
			q := "SELECT id FROM t ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 2"
			want := []int64{2, 3}
			if prepared {
				q = "SELECT id FROM t ORDER BY id OFFSET 1 LIMIT 1 FOR UPDATE SKIP LOCKED"
				want = []int64{3}
			}
			queryLockRows(t, p, b, reader, q, prepared, "id", want...)
			queryLockMigratedBlocked(t, p.ctx, c, b.pg, "UPDATE t SET v=v WHERE id=2", false)
			queryLockMigratedBlocked(t, p.ctx, c, b.pg, "UPDATE t SET v=v WHERE id=3", true)
			referenceRowAffected(t, p.ctx, c, "UPDATE t SET v=v WHERE id=4", false, 1)
			queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", false, "id", 1)
			if err := reader.Rollback(); err != nil {
				t.Fatal(err)
			}
		}
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		holder = queryLockBegin(t, p.ctx, a)
		queryLockRows(t, p, b, holder, "SELECT b.v FROM t a JOIN u b USING(id) WHERE a.id=1 FOR UPDATE OF a SKIP LOCKED FOR SHARE OF a NOWAIT", true, "v", 10)
		referenceRowAffected(t, p.ctx, peer, "UPDATE u SET v=v WHERE id=1", true, 1)
		queryLockMigratedBlocked(t, p.ctx, peer, b.pg, "UPDATE t SET v=v WHERE id=1", false)
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceLockExec(t, p.ctx, a, "CREATE VIEW stored AS SELECT a.id,a.v FROM t a JOIN u b USING(id) FOR UPDATE OF a SKIP LOCKED FOR SHARE OF a NOWAIT", 0)
		referenceLockExec(t, p.ctx, a, "CREATE VIEW nested AS SELECT id,v FROM stored", 0)
		if !b.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if p.postgres != nil {
				if _, err := p.postgres.ExecContext(p.ctx, "DROP VIEW IF EXISTS nested,stored; DROP TABLE IF EXISTS t,u"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT id FROM nested ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
				if _, err := p.postgres.ExecContext(p.ctx, "DROP VIEW nested,stored; DROP TABLE t,u"); err != nil {
					t.Fatal(err)
				}
			}
		}
		holder = queryLockBegin(t, p.ctx, a)
		queryLockRows(t, p, b, holder, "SELECT id FROM nested WHERE id=1", true, "id", 1)
		referenceRowAffected(t, p.ctx, peer, "UPDATE u SET v=v WHERE id=1", false, 1)
		queryLockMigratedBlocked(t, p.ctx, peer, b.pg, "UPDATE t SET v=v WHERE id=1", true)
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		holder = queryLockBegin(t, p.ctx, a)
		queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR NO KEY UPDATE", false, "id", 1)
		for _, tail := range []string{"FOR UPDATE SKIP LOCKED FOR SHARE NOWAIT", "FOR SHARE NOWAIT FOR UPDATE SKIP LOCKED"} {
			queryLockNowait(t, p, b, peer, "SELECT id FROM t WHERE id=1 "+tail, true, "t")
		}
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		for _, prepared := range []bool{false, true} {
			stage := "execute"
			if prepared {
				stage = "prepare"
			}
			q := "SELECT id FROM t FOR UPDATE OF missing"
			queryLockFailure(t, p, b, peer, q, prepared, referenceDiagnostic{Code: "42P01", Message: `relation "missing" in FOR UPDATE clause not found in FROM clause`, Position: queryLockPosition(q, "missing")}, stage)
			queryLockFailure(t, p, b, peer, "SELECT DISTINCT id FROM t FOR SHARE", prepared, referenceDiagnostic{Code: "0A000", Message: "FOR SHARE is not allowed with DISTINCT clause"}, stage)
			queryLockFailure(t, p, b, peer, "SELECT id FROM t UNION SELECT id FROM u FOR UPDATE", prepared, referenceDiagnostic{Code: "0A000", Message: "FOR UPDATE is not allowed with UNION/INTERSECT/EXCEPT"}, stage)
			queryLockRows(t, p, b, peer, "SELECT a.id FROM t a LEFT JOIN u b USING(id) ORDER BY a.id FOR UPDATE OF a", prepared, "id", 1, 2, 3, 4)
			queryLockFailure(t, p, b, peer, "SELECT a.id FROM t a LEFT JOIN u b USING(id) LIMIT 0 FOR UPDATE OF b", prepared, referenceDiagnostic{Code: "0A000", Message: "FOR UPDATE cannot be applied to the nullable side of an outer join"}, "execute")
		}
		referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v", false, 4)
		ap, bp := 0, 0
		if b.pg {
			ap, bp = referenceRowPIDs(t, p.ctx, a, peer)
		}
		holder = queryLockBegin(t, p.ctx, a)
		queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE", false, "id", 2)
		reader := queryLockBegin(t, p.ctx, peer)
		q := "SELECT id FROM t WHERE id<3 ORDER BY id FOR UPDATE OF t"
		var out relationOutcome
		if b.pg {
			done := referenceRowAsync(t, p.ctx, reader, q, true, true, b.recorder.rawBackend)
			referenceRowWait(t, p.ctx, bp, ap)
			queryLockNowait(t, p, b, c, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", false, "t")
			var canceled bool
			if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
				t.Fatal(canceled, err)
			}
			out = referenceLockResult(t, done)
			terminal := queryLockErrorOutcome(t, out, true, "id", []int64{1})
			referenceRowTimeoutDiagnostic(t, terminal, true, "57014", "canceling statement due to user request", "locking")
		} else {
			ctx, cancel := context.WithTimeout(p.ctx, 30*time.Millisecond)
			out = referenceRowQuery(ctx, reader, q, true)
			cancel()
			terminal := queryLockErrorOutcome(t, out, false, "id", nil)
			if !errors.Is(terminal, context.DeadlineExceeded) {
				t.Fatal(terminal)
			}
			var state interface{ SQLState() string }
			if errors.As(terminal, &state) {
				t.Fatal("fabricated caller SQLSTATE", terminal)
			}
		}
		if err := reader.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v WHERE id=1", true, 1)
		queryLockNowait(t, p, b, peer, "SELECT id FROM t WHERE id=2 FOR UPDATE NOWAIT", true, "t")
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v WHERE id<3", false, 2)
	})
	t.Run("inherited-view-bounds", queryLockInheritedViewBounds)
	t.Run("view-target-occurrences", queryLockViewTargetOccurrences)
	t.Run("distinct-frontier", queryLockDistinctFrontier)
}
