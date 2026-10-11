package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceQueryLockFeatureSmoke(t *testing.T) {
	fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		holder := fixture.QueryLockBegin(t, p.Ctx, a)
		fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
		for _, prepared := range []bool{false, true} {
			reader := fixture.QueryLockBegin(t, p.Ctx, peer)
			fixture.QueryLockNowait(t, p, b, reader, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", prepared, "t")
			if err := reader.Rollback(); err != nil {
				t.Fatal(err)
			}
		}
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		for _, prepared := range []bool{false, true} {
			reader := fixture.QueryLockBegin(t, p.Ctx, peer)
			q := "SELECT id FROM t ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 2"
			want := []int64{2, 3}
			if prepared {
				q = "SELECT id FROM t ORDER BY id OFFSET 1 LIMIT 1 FOR UPDATE SKIP LOCKED"
				want = []int64{3}
			}
			fixture.QueryLockRows(t, p, b, reader, q, prepared, "id", want...)
			queryLockMigratedBlocked(t, p.Ctx, c, b.PG, "UPDATE t SET v=v WHERE id=2", false)
			queryLockMigratedBlocked(t, p.Ctx, c, b.PG, "UPDATE t SET v=v WHERE id=3", true)
			fixture.RowAffected(t, p.Ctx, c, "UPDATE t SET v=v WHERE id=4", false, 1)
			fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", false, "id", 1)
			if err := reader.Rollback(); err != nil {
				t.Fatal(err)
			}
		}
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		holder = fixture.QueryLockBegin(t, p.Ctx, a)
		fixture.QueryLockRows(t, p, b, holder, "SELECT b.v FROM t a JOIN u b USING(id) WHERE a.id=1 FOR UPDATE OF a SKIP LOCKED FOR SHARE OF a NOWAIT", true, "v", 10)
		fixture.RowAffected(t, p.Ctx, peer, "UPDATE u SET v=v WHERE id=1", true, 1)
		queryLockMigratedBlocked(t, p.Ctx, peer, b.PG, "UPDATE t SET v=v WHERE id=1", false)
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		fixture.LockExec(t, p.Ctx, a, "CREATE VIEW stored AS SELECT a.id,a.v FROM t a JOIN u b USING(id) FOR UPDATE OF a SKIP LOCKED FOR SHARE OF a NOWAIT", 0)
		fixture.LockExec(t, p.Ctx, a, "CREATE VIEW nested AS SELECT id,v FROM stored", 0)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW IF EXISTS nested,stored; DROP TABLE IF EXISTS t,u"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT id FROM nested ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW nested,stored; DROP TABLE t,u"); err != nil {
					t.Fatal(err)
				}
			}
		}
		holder = fixture.QueryLockBegin(t, p.Ctx, a)
		fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM nested WHERE id=1", true, "id", 1)
		fixture.RowAffected(t, p.Ctx, peer, "UPDATE u SET v=v WHERE id=1", false, 1)
		queryLockMigratedBlocked(t, p.Ctx, peer, b.PG, "UPDATE t SET v=v WHERE id=1", true)
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		holder = fixture.QueryLockBegin(t, p.Ctx, a)
		fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR NO KEY UPDATE", false, "id", 1)
		for _, tail := range []string{"FOR UPDATE SKIP LOCKED FOR SHARE NOWAIT", "FOR SHARE NOWAIT FOR UPDATE SKIP LOCKED"} {
			fixture.QueryLockNowait(t, p, b, peer, "SELECT id FROM t WHERE id=1 "+tail, true, "t")
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
			fixture.QueryLockFailure(t, p, b, peer, q, prepared, fixture.Diagnostic{Code: "42P01", Message: `relation "missing" in FOR UPDATE clause not found in FROM clause`, Position: queryLockPosition(q, "missing")}, stage)
			fixture.QueryLockFailure(t, p, b, peer, "SELECT DISTINCT id FROM t FOR SHARE", prepared, fixture.Diagnostic{Code: "0A000", Message: "FOR SHARE is not allowed with DISTINCT clause"}, stage)
			fixture.QueryLockFailure(t, p, b, peer, "SELECT id FROM t UNION SELECT id FROM u FOR UPDATE", prepared, fixture.Diagnostic{Code: "0A000", Message: "FOR UPDATE is not allowed with UNION/INTERSECT/EXCEPT"}, stage)
			fixture.QueryLockRows(t, p, b, peer, "SELECT a.id FROM t a LEFT JOIN u b USING(id) ORDER BY a.id FOR UPDATE OF a", prepared, "id", 1, 2, 3, 4)
			fixture.QueryLockFailure(t, p, b, peer, "SELECT a.id FROM t a LEFT JOIN u b USING(id) LIMIT 0 FOR UPDATE OF b", prepared, fixture.Diagnostic{Code: "0A000", Message: "FOR UPDATE cannot be applied to the nullable side of an outer join"}, "execute")
		}
		fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v", false, 4)
		ap, bp := 0, 0
		if b.PG {
			ap, bp = fixture.RowPIDs(t, p.Ctx, a, peer)
		}
		holder = fixture.QueryLockBegin(t, p.Ctx, a)
		fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE", false, "id", 2)
		reader := fixture.QueryLockBegin(t, p.Ctx, peer)
		q := "SELECT id FROM t WHERE id<3 ORDER BY id FOR UPDATE OF t"
		var out fixture.RelationOutcome
		if b.PG {
			done := fixture.RowAsync(t, p.Ctx, reader, q, true, true, b.Recorder.RawBackend)
			fixture.RowWait(t, p.Ctx, bp, ap)
			fixture.QueryLockNowait(t, p, b, c, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", false, "t")
			var canceled bool
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
				t.Fatal(canceled, err)
			}
			out = fixture.LockResult(t, done)
			terminal := fixture.QueryLockErrorOutcome(t, out, true, "id", []int64{1})
			referenceRowTimeoutDiagnostic(t, terminal, true, "57014", "canceling statement due to user request", "locking")
		} else {
			ctx, cancel := context.WithTimeout(p.Ctx, 30*time.Millisecond)
			out = fixture.RowQuery(ctx, reader, q, true)
			cancel()
			terminal := fixture.QueryLockErrorOutcome(t, out, false, "id", nil)
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
		fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v WHERE id=1", true, 1)
		fixture.QueryLockNowait(t, p, b, peer, "SELECT id FROM t WHERE id=2 FOR UPDATE NOWAIT", true, "t")
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v WHERE id<3", false, 2)
	})
	t.Run("inherited-view-bounds", queryLockInheritedViewBounds)
	t.Run("view-target-occurrences", queryLockViewTargetOccurrences)
	t.Run("distinct-frontier", queryLockDistinctFrontier)
}
