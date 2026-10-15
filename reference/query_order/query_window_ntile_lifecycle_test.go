package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowNtileHeld(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	q := "SELECT ntile($1) OVER(ORDER BY id) AS n FROM nt WHERE id<=5 ORDER BY id"
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
	for _, tc := range []struct {
		arg    any
		labels []any
		fails  bool
	}{
		{2, []any{int64(1), int64(1), int64(1), int64(2), int64(2)}, false},
		{3, []any{int64(1), int64(1), int64(2), int64(2), int64(3)}, false},
		{nil, []any{nil, nil, nil, nil, nil}, false},
		{0, nil, true}, {-1, nil, true},
		{2, []any{int64(1), int64(1), int64(1), int64(2), int64(2)}, false},
	} {
		rows := make([][]any, len(tc.labels))
		for i, v := range tc.labels {
			rows[i] = []any{v}
		}
		s := fixture.Select(q, []string{"n"}, []string{"INT4"}, rows, tc.arg)
		if tc.fails {
			s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "22014", Message: "argument of ntile must be greater than zero"}, tc.arg)
			s.SelectRows, s.ErrorStage = true, "execute"
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT ntile(2) OVER() AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
}

func queryWindowNtileAtomic(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE nt_dst(v INT)", 0))
	queryWindowRun(t, p, b, fixture.Exec("INSERT INTO nt_dst VALUES(91)", 1))
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError("INSERT INTO nt_dst SELECT ntile(CASE WHEN p='b' THEN 0 ELSE 2 END) OVER(PARTITION BY p ORDER BY id) FROM nt", fixture.Diagnostic{Code: "22014", Message: "argument of ntile must be greater than zero"})
		s.Prepared, s.ErrorStage = prepared, "execute"
		queryWindowRun(t, p, b, s)
		queryWindowRoutes(t, p, b, fixture.Select("SELECT v FROM nt_dst ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		s = fixture.Exec("INSERT INTO nt_dst SELECT ntile(2) OVER(ORDER BY id) FROM nt WHERE id<=5", 5)
		s.Prepared = prepared
		queryWindowRun(t, p, b, s)
		queryWindowRun(t, p, b, fixture.Select("SELECT v FROM nt_dst ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(91)}}))
		queryWindowRun(t, p, b, fixture.Exec("DELETE FROM nt_dst WHERE v<>91", 5))
		s = fixture.Exec("INSERT INTO nt_dst SELECT ntile(0) OVER() FROM nt WHERE FALSE", 0)
		s.Prepared = prepared
		queryWindowRun(t, p, b, s)
	}
}

func TestReferenceQueryWindowNtileLifecycle(t *testing.T) {
	queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowNtileSeed(t, p, b)
		t.Run("held", func(t *testing.T) { queryWindowNtileHeld(t, p, b) })
		t.Run("atomic", func(t *testing.T) { queryWindowNtileAtomic(t, p, b) })
		t.Run("cancellation", func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			q := "SELECT ntile($1) OVER(ORDER BY id) FROM nt"
			if b.PG {
				for _, prepared := range []bool{false, true} {
					fixture.SessionCancelQuery(t, p.Ctx, conn, prepared, "SELECT ntile((SELECT 2 FROM pg_sleep(600))) OVER()", true)
				}
			} else {
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
						rows, err = stmt.QueryContext(ctx, 2)
					} else {
						rows, err = conn.QueryContext(ctx, q, 2)
					}
					if rows != nil {
						rows.Close()
						t.Fatal("precanceled ntile exposed rows")
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
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT ntile(2) OVER() AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
			queryWindowRun(t, p, b, fixture.Select("SELECT v FROM nt_dst", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		})
	})
}
