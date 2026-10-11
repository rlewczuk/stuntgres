package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowRangeMetadata(t *testing.T) {
	queryWindowRangeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRangeSeed(t, p, b)
		for _, suffix := range []string{"", " WHERE FALSE"} {
			s := queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k RANGE BETWEEN 1 PRECEDING AND 2 PRECEDING) AS n FROM range_t" + suffix + " ORDER BY id")
			if suffix == "" {
				s.Rows = queryWindowValues(0, 0, 0, 0, 0, 2, 2)
			}
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				cols, err := rows.ColumnTypes()
				if err != nil || len(cols) != 2 {
					t.Fatal(cols, err)
				}
				if cols[1].ScanType() != reflect.TypeFor[int64]() {
					t.Fatal(cols[1].ScanType())
				}
				nullable, known := cols[1].Nullable()
				if nullable || known == b.PG {
					t.Fatal(nullable, known)
				}
				if n, ok := cols[1].Length(); n != 0 || ok {
					t.Fatal(n, ok)
				}
				if p, s, ok := cols[1].DecimalSize(); p != 0 || s != 0 || ok {
					t.Fatal(p, s, ok)
				}
			}
			s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
				if column != 1 {
					return false
				}
				if _, ok := v.(int64); !ok {
					t.Fatalf("count raw %T", v)
				}
				return true
			}
			s.CheckScan = func(t *testing.T, rows *sql.Rows, row int) {
				var id int64
				var n sql.NullInt64
				if err := rows.Scan(&id, &n); err != nil || !n.Valid || n.Int64 != s.Rows[row][1] {
					t.Fatal(id, n, err)
				}
			}
			queryWindowRoutes(t, p, b, s)
		}
	})
}

func TestReferenceQueryWindowRangeAtomicity(t *testing.T) {
	queryWindowRangeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRangeSeed(t, p, b)
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE range_dst(n BIGINT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO range_dst VALUES(99)", 1))
		sentinel := fixture.Select("SELECT n FROM range_dst ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(99)}})
		for _, tc := range []struct{ q, code, message string }{
			{"INSERT INTO range_dst SELECT count(*) OVER(ORDER BY k RANGE -1 PRECEDING) FROM range_t", "22013", "invalid preceding or following size in window function"},
			{"INSERT INTO range_dst SELECT count(*) OVER(ORDER BY k RANGE NULL PRECEDING) FROM range_t", "22004", "frame starting offset must not be null"},
			{"INSERT INTO range_dst SELECT count(*) OVER(ORDER BY k RANGE (SELECT 1/0) PRECEDING) FROM range_t", "22012", "division by zero"},
			{"INSERT INTO range_dst SELECT count(*) OVER(ORDER BY 1/(id-3) RANGE 1 PRECEDING) FROM range_t", "22012", "division by zero"},
			{"INSERT INTO range_dst SELECT count(*) OVER(ORDER BY DATE '294276-12-31' RANGE BETWEEN CURRENT ROW AND INTERVAL '1 year' FOLLOWING) FROM range_t", "22008", "timestamp out of range"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				queryWindowRun(t, p, b, s)
				queryWindowRoutes(t, p, b, sentinel)
			}
		}
		// Full-input parsing must not publish the valid prefix.
		q := "INSERT INTO range_dst VALUES(1); SELECT count(*) OVER(ORDER BY k RANGE 1 PRECEDING) FROM range_t; SELECT ("
		// The malformed script diagnostic itself is independently asserted at '(' EOF.
		for _, prepared := range []bool{false, true} {
			malformed := q
			if prepared {
				malformed = "SELECT ("
			}
			s := fixture.ImplicitError(malformed, fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: fixture.Position(len([]rune(malformed)) + 1)})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			queryWindowRun(t, p, b, s)
			queryWindowRoutes(t, p, b, sentinel)
		}
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if b.PG {
			for _, prepared := range []bool{false, true} {
				fixture.SessionCancelQuery(t, p.Ctx, conn, prepared, "SELECT count(*) OVER(ORDER BY k RANGE (SELECT 1 FROM (SELECT pg_sleep(600)) AS sleeping) PRECEDING) FROM range_t", true)
			}
		} else {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				q := "INSERT INTO range_dst SELECT count(*) OVER(ORDER BY k RANGE 1 PRECEDING) FROM range_t"
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				var result sql.Result
				if prepared {
					result, err = stmt.ExecContext(ctx)
				} else {
					result, err = conn.ExecContext(ctx, q)
				}
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatal(result, err)
				}
				stmt.Close()
			}
		}
		queryWindowRoutes(t, p, b, sentinel)
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO range_dst SELECT count(*) OVER(ORDER BY k RANGE 1 PRECEDING) FROM range_t", 7))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT n FROM range_dst ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}, {int64(99)}}))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO range_dst SELECT count(*) OVER(ORDER BY k RANGE 1 PRECEDING) FROM range_t WHERE FALSE", 0))
	})
}

func TestReferenceQueryWindowRangeAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"range_reader"})
	queryWindowRangeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRun(t, p, b, fixture.Exec("CREATE ROLE range_reader", 0))
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE range_secret(v INT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO range_secret VALUES(1)", 1))
		// The reset PostgreSQL public schema has no PUBLIC USAGE. Establish name
		// visibility separately from relation SELECT authority, as in the window ACL fixture.
		if b.PG {
			queryWindowRun(t, p, b, fixture.Exec("GRANT USAGE ON SCHEMA public TO range_reader", 0))
		}
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION range_reader", 0))
		for _, q := range []string{
			"SELECT count(*) OVER(ORDER BY v RANGE 1 PRECEDING) FROM public.range_secret",
			"SELECT count(*) OVER(ORDER BY 1 RANGE (SELECT v FROM public.range_secret) PRECEDING)",
			"SELECT 1 WINDOW unused AS(ORDER BY 1 RANGE (SELECT v FROM public.range_secret) PRECEDING)",
			"SELECT count(*) OVER(ORDER BY v RANGE 1 PRECEDING) FROM public.range_secret WHERE FALSE",
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table range_secret"})
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				// Both native and PostgreSQL defer relation ACL checks to Execute,
				// including fresh prepared SELECTs (the established window ACL contract).
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT v FROM range_secret", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
