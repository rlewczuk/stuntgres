package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowRankHeld(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	q := "SELECT id," + queryWindowRankProjection("(ORDER BY k)") + " FROM t WHERE p='a' AND id>$1 ORDER BY id"
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
	for _, tc := range []struct {
		arg  int
		rows [][]any
	}{
		{0, queryWindowRankRows()[:3]},
		{1, [][]any{{int64(2), int64(1), int64(1), float64(0), float64(.5)}, {int64(3), int64(2), int64(2), float64(1), float64(1)}}},
		{2, [][]any{{int64(3), int64(1), int64(1), float64(0), float64(1)}}},
		{3, nil}, {0, queryWindowRankRows()[:3]},
	} {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, queryWindowRankResult(q, tc.rows, tc.arg))
	}
}

func queryWindowRankAtomic(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE rank_dst(id INT,r BIGINT,d BIGINT,pr DOUBLE PRECISION,cd DOUBLE PRECISION)", 0))
	queryWindowRun(t, p, b, fixture.Exec("INSERT INTO rank_dst VALUES(17,17,17,17,17)", 1))
	for _, prepared := range []bool{false, true} {
		for _, limit := range []string{"", " LIMIT 1"} {
			q := "SELECT rank() OVER(ORDER BY 1/(id-3)) FROM t" + limit
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			queryWindowRun(t, p, b, s)
		}
		s := fixture.ImplicitError("INSERT INTO rank_dst(id,r) SELECT id,rank() OVER(ORDER BY 1/(id-3)) FROM t", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		s.Prepared, s.ErrorStage = prepared, "execute"
		queryWindowRun(t, p, b, s)
		queryWindowRoutes(t, p, b, queryWindowRankResult("SELECT * FROM rank_dst", [][]any{{int64(17), int64(17), int64(17), float64(17), float64(17)}}))
		s = fixture.Exec("INSERT INTO rank_dst SELECT id,"+queryWindowRankProjection("(PARTITION BY p ORDER BY k)")+" FROM t", 7)
		s.Prepared = prepared
		queryWindowRun(t, p, b, s)
		queryWindowRoutes(t, p, b, queryWindowRankResult("SELECT * FROM rank_dst WHERE id<>17 ORDER BY id", queryWindowRankRows()))
		queryWindowRun(t, p, b, fixture.Exec("DELETE FROM rank_dst WHERE id<>17", 7))
		s = fixture.Exec("INSERT INTO rank_dst SELECT id,"+queryWindowRankProjection("(PARTITION BY p ORDER BY k)")+" FROM empty_t", 0)
		s.Prepared = prepared
		queryWindowRun(t, p, b, s)
	}
}

func queryWindowRankCancel(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if b.PG {
		for _, prepared := range []bool{false, true} {
			fixture.SessionCancelQuery(t, p.Ctx, conn, prepared, "SELECT rank() OVER(ORDER BY (SELECT 1 FROM pg_sleep(600)))", true)
		}
	} else {
		q := "SELECT " + queryWindowRankProjection("(ORDER BY k)") + " FROM t"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			if prepared {
				rows, err = stmt.QueryContext(ctx)
			} else {
				rows, err = conn.QueryContext(ctx, q)
			}
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled ranking exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("fabricated SQLSTATE", err)
			}
		}
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT "+queryWindowRankProjection("()"), []string{"r", "d", "pr", "cd"}, []string{"INT8", "INT8", "FLOAT8", "FLOAT8"}, [][]any{{int64(1), int64(1), float64(0), float64(1)}}))
}

func TestReferenceQueryWindowRankLifecycle(t *testing.T) {
	queryWindowRankScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowSeed(t, p, b)
		t.Run("held", func(t *testing.T) { queryWindowRankHeld(t, p, b) })
		t.Run("atomic", func(t *testing.T) { queryWindowRankAtomic(t, p, b) })
		t.Run("cancellation", func(t *testing.T) { queryWindowRankCancel(t, p, b) })
	})
}
