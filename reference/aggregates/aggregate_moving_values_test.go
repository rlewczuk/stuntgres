package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const movingWindow = "SELECT id,ma_sum(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY id"

func movingSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	lifecycleExec(t, p, b, c,
		"CREATE AGGREGATE ma_sum(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=int4,MINITCOND='0')",
		"CREATE TABLE ma_input(id integer,x integer)")
	createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO ma_input VALUES(1,2),(2,3),(3,5),(4,7)", 4))
}

func movingRows(q string, values ...any) fixture.Step {
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{int64(i + 1), v}
	}
	return fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, rows)
}

func movingMetadata(pg bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(cols) != 2 {
			t.Fatalf("columns=%d", len(cols))
		}
		for i, col := range cols {
			want := reflect.TypeOf(int64(0))
			if pg {
				want = reflect.TypeOf(int32(0))
			}
			if col.ScanType() != want {
				t.Fatalf("column %d scan=%v want %v", i, col.ScanType(), want)
			}
			if n, ok := col.Length(); n != 0 || ok {
				t.Fatalf("column %d length=%d/%v", i, n, ok)
			}
			if p, s, ok := col.DecimalSize(); p != 0 || s != 0 || ok {
				t.Fatalf("column %d decimal=%d/%d/%v", i, p, s, ok)
			}
			if n, ok := col.Nullable(); n != !pg || ok != !pg {
				t.Fatalf("column %d nullable=%v/%v", i, n, ok)
			}
		}
	}
}

func TestReferenceAggregateMovingValues(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		movingSetup(t, p, b, c)
		createAggregateBoth(t, p, b, c, movingRows(movingWindow, int64(2), int64(5), int64(8), int64(12)))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ma_sum(x) AS v FROM ma_input", int64(17)))
		lifecycleExec(t, p, b, c, "CREATE TABLE ma_destination(id integer,v integer)")
		createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO ma_destination "+movingWindow, 4))
		createAggregateBoth(t, p, b, c, movingRows("SELECT id,v FROM ma_destination ORDER BY id", int64(2), int64(5), int64(8), int64(12)))
	})
}

func TestReferenceAggregateMovingFrames(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		movingSetup(t, p, b, c)
		for _, tc := range []struct {
			frame  string
			values []any
		}{
			{"ROWS BETWEEN CURRENT ROW AND 1 FOLLOWING", []any{int64(5), int64(8), int64(12), int64(7)}},
			{"ROWS CURRENT ROW", []any{int64(2), int64(3), int64(5), int64(7)}},
			{"ROWS BETWEEN 1 PRECEDING AND 1 PRECEDING", []any{int64(0), int64(2), int64(3), int64(5)}},
			{"ROWS BETWEEN 1 PRECEDING AND 2 PRECEDING", []any{int64(0), int64(0), int64(0), int64(0)}},
			{"ROWS BETWEEN 1 PRECEDING AND CURRENT ROW EXCLUDE CURRENT ROW", []any{int64(0), int64(2), int64(3), int64(5)}},
		} {
			createAggregateBoth(t, p, b, c, movingRows("SELECT id,ma_sum(x) OVER(ORDER BY id "+tc.frame+") AS v FROM ma_input ORDER BY id", tc.values...))
		}
		createAggregateBoth(t, p, b, c, movingRows("SELECT id,ma_sum(x) OVER(PARTITION BY id/3 ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY id", int64(2), int64(5), int64(5), int64(12)))
		for _, unit := range []string{"RANGE", "GROUPS"} {
			createAggregateBoth(t, p, b, c, movingRows("SELECT id,ma_sum(x) OVER(ORDER BY id/2 "+unit+" BETWEEN 1 PRECEDING AND CURRENT ROW) AS v FROM ma_input ORDER BY id", int64(2), int64(10), int64(10), int64(15)))
		}
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT id,ma_sum(x) OVER w AS a,ma_sum(x+1) OVER w AS b,sum(x) OVER w AS s,count(*) OVER w AS n,row_number() OVER w AS r FROM ma_input WINDOW w AS(ORDER BY id ROWS 1 PRECEDING) ORDER BY id", []string{"id", "a", "b", "s", "n", "r"}, []string{"INT4", "INT4", "INT4", "INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(2), int64(3), int64(2), int64(1), int64(1)}, {int64(2), int64(5), int64(7), int64(5), int64(2), int64(2)}, {int64(3), int64(8), int64(10), int64(8), int64(2), int64(3)}, {int64(4), int64(12), int64(14), int64(12), int64(2), int64(4)}}))
	})
}

func TestReferenceAggregateMovingPrepared(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		movingSetup(t, p, b, c)
		const q = "SELECT id,ma_sum(x) OVER(ORDER BY id ROWS $1 PRECEDING) AS v FROM ma_input ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			offset        any
			values        []any
			code, message string
		}{
			{int64(0), []any{int64(2), int64(3), int64(5), int64(7)}, "", ""},
			{int64(1), []any{int64(2), int64(5), int64(8), int64(12)}, "", ""},
			{int64(2), []any{int64(2), int64(5), int64(10), int64(15)}, "", ""},
			{nil, nil, "22004", "frame starting offset must not be null"},
			{int64(-1), nil, "22013", "frame starting offset must not be negative"},
			{int64(1), []any{int64(2), int64(5), int64(8), int64(12)}, "", ""},
		} {
			s := movingRows(q, tc.values...)
			s.Args = []any{tc.offset}
			if tc.code != "" {
				s = fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message}, tc.offset)
				s.SelectRows, s.ErrorStage = true, "execute"
			} else {
				s.CheckMetadata = movingMetadata(b.PG)
			}
			createAggregateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	})
}

func TestReferenceAggregateMovingMetadata(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		movingSetup(t, p, b, c)
		for _, empty := range []bool{false, true} {
			q := "SELECT id,ma_sum(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input"
			values := []any{int64(2), int64(5), int64(8), int64(12)}
			if empty {
				q += " WHERE FALSE"
				values = nil
			}
			s := movingRows(q+" ORDER BY id", values...)
			s.CheckMetadata = movingMetadata(b.PG)
			createAggregateBoth(t, p, b, c, s)
			stmt, err := c.PrepareContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}, b.Recorder, s)
			stmt.Close()
		}
	})
}
