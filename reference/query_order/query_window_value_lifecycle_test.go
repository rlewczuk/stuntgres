package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowValueHeld(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	q := "SELECT nth_value(v,$1) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS n FROM wv WHERE id<=4 ORDER BY id"
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
	for _, tc := range []struct {
		arg, value any
		fails      bool
	}{
		{1, int64(10), false}, {2, int64(10), false}, {100, nil, false}, {nil, nil, false}, {0, nil, true}, {-1, nil, true}, {1, int64(10), false},
	} {
		s := fixture.Select(q, []string{"n"}, []string{"INT4"}, [][]any{{tc.value}, {tc.value}, {tc.value}, {tc.value}}, tc.arg)
		if tc.fails {
			s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "22016", Message: "argument of nth_value must be greater than zero"}, tc.arg)
			s.SelectRows, s.ErrorStage = true, "execute"
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}

func TestReferenceQueryWindowValueLifecycle(t *testing.T) {
	queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowValueSeed(t, p, b)
		t.Run("held", func(t *testing.T) { queryWindowValueHeld(t, p, b) })
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE wv_dst(v INT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO wv_dst VALUES(91)", 1))
		for i, tc := range []struct{ q, code, message string }{
			{"INSERT INTO wv_dst SELECT nth_value(v,4-id) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM wv WHERE id<=4", "22016", "argument of nth_value must be greater than zero"},
			{"INSERT INTO wv_dst SELECT last_value(1/(id-3)) OVER(ORDER BY id ROWS CURRENT ROW) FROM wv WHERE id<=4", "22012", "division by zero"},
			{"SELECT nth_value(1/(id-3),3) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM wv WHERE id<=4", "22012", "division by zero"},
		} {
			t.Run(fmt.Sprintf("atomic-%d", i), func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared, s.ErrorStage, s.SelectRows = prepared, "execute", i == 2
					queryWindowRun(t, p, b, s)
					queryWindowRoutes(t, p, b, fixture.Select("SELECT v FROM wv_dst ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
				}
			})
		}
		t.Run("insert-success", func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("INSERT INTO wv_dst SELECT first_value(id) OVER(ORDER BY id) FROM wv WHERE id<=4", 4)
				s.Prepared = prepared
				queryWindowRun(t, p, b, s)
				queryWindowRun(t, p, b, fixture.Select("SELECT v FROM wv_dst ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(91)}}))
				queryWindowRun(t, p, b, fixture.Exec("DELETE FROM wv_dst WHERE v=1", 4))
				s = fixture.Exec("INSERT INTO wv_dst SELECT nth_value(id,0) OVER() FROM wv WHERE FALSE", 0)
				s.Prepared = prepared
				queryWindowRun(t, p, b, s)
			}
		})
		t.Run("cancellation", func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			q := "SELECT first_value(id) OVER(),last_value(id) OVER(),nth_value(id,$1) OVER() FROM wv ORDER BY id"
			if b.PG {
				for _, prepared := range []bool{false, true} {
					fixture.SessionCancel(t, p.Ctx, conn, prepared, true)
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
						rows, err = stmt.QueryContext(ctx, 1)
					} else {
						rows, err = conn.QueryContext(ctx, q, 1)
					}
					if rows != nil {
						rows.Close()
						t.Fatal("precanceled query published rows")
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
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT first_value(1) OVER(),last_value(1) OVER(),nth_value(1,$1) OVER()", []string{"first_value", "last_value", "nth_value"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1)}}, 1))
			queryWindowRun(t, p, b, fixture.Select("SELECT v FROM wv_dst", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		})
	})
}
