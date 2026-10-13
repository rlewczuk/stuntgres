package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

// PostgreSQL may publish streaming rows before a late execution error. Native
// execution is eager and must reject the query without publishing a cursor.
func queryWindowExcludeRuntime(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q string, prepared bool, code, message string) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, prepareErr := x.PrepareContext(p.Ctx, q)
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.Ctx)
	} else {
		rows, err = x.QueryContext(p.Ctx, q)
	}
	if rows != nil {
		defer rows.Close()
		if !b.PG {
			t.Fatal("failed native window query exposed a cursor")
		}
		if err != nil {
			t.Fatal("failed Query exposed a cursor", err)
		}
		for rows.Next() {
			var value any
			if scanErr := rows.Scan(&value); scanErr != nil {
				t.Fatal(scanErr)
			}
		}
		err = rows.Err()
		if closeErr := rows.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	want := fixture.ControlsReferenceDiagnostic(code, message)
	fixture.State(t, err, code)
	fixture.AssertDiagnostic(t, err, &want)
}

func TestReferenceQueryWindowExcludeLifecycle(t *testing.T) {
	queryWindowExcludeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE ex_bad(id INT,v TEXT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO ex_bad VALUES(1,'10'),(2,'oops')", 2))
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE ex_dest(v BIGINT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO ex_dest VALUES(99)", 1))
		q := "SELECT sum(v::INT) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW EXCLUDE NO OTHERS) AS s FROM ex_bad ORDER BY id"
		for _, prepared := range []bool{false, true} {
			queryWindowExcludeRuntime(t, p, b, b.DB, q, prepared, "22P02", `invalid input syntax for type integer: "oops"`)
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("INSERT INTO ex_dest SELECT sum(v::INT) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW EXCLUDE NO OTHERS) FROM ex_bad", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			queryWindowRun(t, p, b, s)
			queryWindowRoutes(t, p, b, fixture.Select("SELECT v FROM ex_dest", []string{"v"}, []string{"INT8"}, [][]any{{int64(99)}}))
		}
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{b.DB, stmt, q}
		queryWindowExcludeRuntime(t, p, b, held, q, false, "22P02", `invalid input syntax for type integer: "oops"`)
		queryWindowRun(t, p, b, fixture.Exec("UPDATE ex_bad SET v='20' WHERE id=2", 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"s"}, []string{"INT8"}, [][]any{{int64(10)}, {int64(30)}}))
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE ex_float(id INT,v DOUBLE PRECISION)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO ex_float VALUES(1,1e308),(2,1e308)", 2))
		for _, prepared := range []bool{false, true} {
			queryWindowExcludeRuntime(t, p, b, b.DB, "SELECT sum(v) OVER(ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE NO OTHERS) FROM ex_float", prepared, "22003", "value out of range: overflow")
		}
	})
}

func TestReferenceQueryWindowExcludeCancellation(t *testing.T) {
	queryWindowExcludeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE ex_dest(v BIGINT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO ex_dest VALUES(99)", 1))
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		for _, prepared := range []bool{false, true} {
			if b.PG {
				fixture.SessionCancelQuery(t, p.Ctx, c, prepared, "SELECT sum(v+(SELECT 0 FROM (SELECT pg_sleep(600)) sleeping)) OVER(ORDER BY k ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES) FROM ex_t", true)
				fixture.SessionCancelQuery(t, p.Ctx, c, prepared, "INSERT INTO ex_dest SELECT sum(v+(SELECT 0 FROM (SELECT pg_sleep(600)) sleeping)) OVER(ORDER BY k ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES) FROM ex_t", false)
			} else {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				q := queryWindowExcludeQuery("ORDER BY k ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES")
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(ctx)
				} else {
					rows, err = c.QueryContext(ctx, q)
				}
				stmt.Close()
				if rows != nil || !errors.Is(err, context.Canceled) {
					t.Fatal(rows, err)
				}
				q = "INSERT INTO ex_dest SELECT sum(v) OVER(ORDER BY k ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES) FROM ex_t"
				stmt, err = c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				var result sql.Result
				if prepared {
					result, err = stmt.ExecContext(ctx)
				} else {
					result, err = c.ExecContext(ctx, q)
				}
				stmt.Close()
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatal(result, err)
				}
			}
		}
		queryWindowRoutes(t, p, b, fixture.Select("SELECT v FROM ex_dest", []string{"v"}, []string{"INT8"}, [][]any{{int64(99)}}))
		queryWindowRoutes(t, p, b, queryWindowExcludeResult(queryWindowExcludeQuery("ORDER BY k ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES"), []int64{2, 2, 3}, []any{int64(40), int64(50), int64(60)}))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO ex_dest SELECT sum(v) OVER(ORDER BY k ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES) FROM ex_t", 3))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT v FROM ex_dest ORDER BY v", []string{"v"}, []string{"INT8"}, [][]any{{int64(40)}, {int64(50)}, {int64(60)}, {int64(99)}}))
	})
}
