package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func queryWindowOffsetHeld(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	q := "SELECT lead(v,$1,$2::int) OVER(ORDER BY id) AS x FROM wo ORDER BY id"
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
	for _, tc := range []struct {
		n      any
		values []any
	}{
		{1, []any{nil, int64(30), int64(40), int64(50), nil, int64(99)}},
		{0, []any{int64(10), nil, int64(30), int64(40), int64(50), nil}},
		{-1, []any{int64(99), int64(10), nil, int64(30), int64(40), int64(50)}},
		{nil, []any{nil, nil, nil, nil, nil, nil}},
		{100, []any{int64(99), int64(99), int64(99), int64(99), int64(99), int64(99)}},
		{1, []any{nil, int64(30), int64(40), int64(50), nil, int64(99)}},
	} {
		var rows [][]any
		for _, v := range tc.values {
			rows = append(rows, []any{v})
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"x"}, []string{"INT4"}, rows, tc.n, 99))
	}
	queryWindowRun(t, p, b, fixture.Exec("UPDATE wo SET v=11 WHERE id=1", 1))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"x"}, []string{"INT4"}, [][]any{{int64(11)}, {nil}, {int64(30)}, {int64(40)}, {int64(50)}, {nil}}, 0, 88))
	queryWindowRun(t, p, b, fixture.Exec("UPDATE wo SET v=10 WHERE id=1", 1))
	// Same held runtime plan must recover after a late reached value/default/offset failure.
	for _, expr := range []string{"lead(1/(id-$1),0,0)", "lead(id,100,1/(id-$1))", "lead(id,1/(id-$1),0)"} {
		q := "SELECT " + expr + " OVER(ORDER BY id) AS x FROM wo ORDER BY id DESC"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, 4)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			var rows [][]any
			for id := 6; id >= 1; id-- {
				var v any = int64(0)
				if expr == "lead(id,1/(id-$1),0)" {
					v = int64(id)
				}
				rows = append(rows, []any{v})
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"x"}, []string{"INT4"}, rows, 100))
		}()
	}
}

func queryWindowOffsetAtomic(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE wo_dst(v INT)", 0))
	queryWindowRun(t, p, b, fixture.Exec("INSERT INTO wo_dst VALUES(91)", 1))
	for i, expr := range []string{"lead(1/(id-4),0,0)", "lag(id,100,1/(id-4))", "lead(id,1/(id-4),0)"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("atomic-%d/%t", i, prepared), func(t *testing.T) {
				s := fixture.ImplicitError("INSERT INTO wo_dst SELECT "+expr+" OVER(ORDER BY id) FROM wo", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				queryWindowRun(t, p, b, s)
				queryWindowRoutes(t, p, b, fixture.Select("SELECT v FROM wo_dst ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.Exec("INSERT INTO wo_dst SELECT lead(id,0) OVER() FROM wo", 6)
		s.Prepared = prepared
		queryWindowRun(t, p, b, s)
		queryWindowRoutes(t, p, b, fixture.Select("SELECT v FROM wo_dst ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(91)}}))
		queryWindowRun(t, p, b, fixture.Exec("DELETE FROM wo_dst WHERE v<91", 6))
		s = fixture.Exec("INSERT INTO wo_dst SELECT lead(1/(id-id),0) OVER() FROM wo WHERE FALSE", 0)
		s.Prepared = prepared
		queryWindowRun(t, p, b, s)
	}
}

func TestReferenceQueryWindowOffsetLifecycle(t *testing.T) {
	queryWindowOffsetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowOffsetSeed(t, p, b)
		queryWindowOffsetHeld(t, p, b)
		queryWindowOffsetAtomic(t, p, b)
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		q := "SELECT lead(id,0) OVER() AS x FROM wo ORDER BY id"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if b.PG {
			for _, prepared := range []bool{false, true} {
				fixture.SessionCancel(t, p.Ctx, conn, prepared, true)
			}
		} else {
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
					t.Fatal("canceled cursor published")
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
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"x"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(6)}}))
	})
}
