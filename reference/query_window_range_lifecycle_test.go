package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func TestReferenceQueryWindowRangeMetadata(t *testing.T) {
	queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
		queryWindowRangeSeed(t, p, b)
		for _, suffix := range []string{"", " WHERE FALSE"} {
			s := queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k RANGE BETWEEN 1 PRECEDING AND 2 PRECEDING) AS n FROM range_t" + suffix + " ORDER BY id")
			if suffix == "" {
				s.rows = queryWindowValues(0, 0, 0, 0, 0, 2, 2)
			}
			s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
				cols, err := rows.ColumnTypes()
				if err != nil || len(cols) != 2 {
					t.Fatal(cols, err)
				}
				if cols[1].ScanType() != reflect.TypeFor[int64]() {
					t.Fatal(cols[1].ScanType())
				}
				nullable, known := cols[1].Nullable()
				if nullable || known == b.pg {
					t.Fatal(nullable, known)
				}
				if n, ok := cols[1].Length(); n != 0 || ok {
					t.Fatal(n, ok)
				}
				if p, s, ok := cols[1].DecimalSize(); p != 0 || s != 0 || ok {
					t.Fatal(p, s, ok)
				}
			}
			s.checkRaw = func(t *testing.T, column, row int, v any) bool {
				if column != 1 {
					return false
				}
				if _, ok := v.(int64); !ok {
					t.Fatalf("count raw %T", v)
				}
				return true
			}
			s.checkScan = func(t *testing.T, rows *sql.Rows, row int) {
				var id int64
				var n sql.NullInt64
				if err := rows.Scan(&id, &n); err != nil || !n.Valid || n.Int64 != s.rows[row][1] {
					t.Fatal(id, n, err)
				}
			}
			queryWindowRoutes(t, p, b, s)
		}
	})
}

func TestReferenceQueryWindowRangeAtomicity(t *testing.T) {
	queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
		queryWindowRangeSeed(t, p, b)
		queryWindowRun(t, p, b, referenceExec("CREATE TABLE range_dst(n BIGINT)", 0))
		queryWindowRun(t, p, b, referenceExec("INSERT INTO range_dst VALUES(99)", 1))
		sentinel := referenceSelect("SELECT n FROM range_dst ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(99)}})
		for _, tc := range []struct{ q, code, message string }{
			{"INSERT INTO range_dst SELECT count(*) OVER(ORDER BY k RANGE -1 PRECEDING) FROM range_t", "22013", "invalid preceding or following size in window function"},
			{"INSERT INTO range_dst SELECT count(*) OVER(ORDER BY k RANGE NULL PRECEDING) FROM range_t", "22004", "frame starting offset must not be null"},
			{"INSERT INTO range_dst SELECT count(*) OVER(ORDER BY k RANGE (SELECT 1/0) PRECEDING) FROM range_t", "22012", "division by zero"},
			{"INSERT INTO range_dst SELECT count(*) OVER(ORDER BY 1/(id-3) RANGE 1 PRECEDING) FROM range_t", "22012", "division by zero"},
			{"INSERT INTO range_dst SELECT count(*) OVER(ORDER BY DATE '294276-12-31' RANGE BETWEEN CURRENT ROW AND INTERVAL '1 year' FOLLOWING) FROM range_t", "22008", "timestamp out of range"},
		} {
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message})
				s.prepared = prepared
				s.errorStage = "execute"
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
			s := referenceImplicitError(malformed, referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: referencePosition(len([]rune(malformed)) + 1)})
			s.prepared = prepared
			s.errorStage = "execute"
			if prepared {
				s.errorStage = "prepare"
			}
			queryWindowRun(t, p, b, s)
			queryWindowRoutes(t, p, b, sentinel)
		}
		conn, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if b.pg {
			for _, prepared := range []bool{false, true} {
				referenceSessionCancelQuery(t, p.ctx, conn, prepared, "SELECT count(*) OVER(ORDER BY k RANGE (SELECT 1 FROM (SELECT pg_sleep(600)) AS sleeping) PRECEDING) FROM range_t", true)
			}
		} else {
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				q := "INSERT INTO range_dst SELECT count(*) OVER(ORDER BY k RANGE 1 PRECEDING) FROM range_t"
				stmt, err := conn.PrepareContext(p.ctx, q)
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
		queryWindowRun(t, p, b, referenceExec("INSERT INTO range_dst SELECT count(*) OVER(ORDER BY k RANGE 1 PRECEDING) FROM range_t", 7))
		queryWindowRoutes(t, p, b, referenceSelect("SELECT n FROM range_dst ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}, {int64(99)}}))
		queryWindowRun(t, p, b, referenceExec("INSERT INTO range_dst SELECT count(*) OVER(ORDER BY k RANGE 1 PRECEDING) FROM range_t WHERE FALSE", 0))
	})
}

func TestReferenceQueryWindowRangeAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"range_reader"})
	queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
		queryWindowRun(t, p, b, referenceExec("CREATE ROLE range_reader", 0))
		queryWindowRun(t, p, b, referenceExec("CREATE TABLE range_secret(v INT)", 0))
		queryWindowRun(t, p, b, referenceExec("INSERT INTO range_secret VALUES(1)", 1))
		// The reset PostgreSQL public schema has no PUBLIC USAGE. Establish name
		// visibility separately from relation SELECT authority, as in the window ACL fixture.
		if b.pg {
			queryWindowRun(t, p, b, referenceExec("GRANT USAGE ON SCHEMA public TO range_reader", 0))
		}
		c, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET SESSION AUTHORIZATION range_reader", 0))
		for _, q := range []string{
			"SELECT count(*) OVER(ORDER BY v RANGE 1 PRECEDING) FROM public.range_secret",
			"SELECT count(*) OVER(ORDER BY 1 RANGE (SELECT v FROM public.range_secret) PRECEDING)",
			"SELECT 1 WINDOW unused AS(ORDER BY 1 RANGE (SELECT v FROM public.range_secret) PRECEDING)",
			"SELECT count(*) OVER(ORDER BY v RANGE 1 PRECEDING) FROM public.range_secret WHERE FALSE",
		} {
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table range_secret"})
				s.selectRows = true
				s.prepared = prepared
				s.errorStage = "execute"
				// Both native and PostgreSQL defer relation ACL checks to Execute,
				// including fresh prepared SELECTs (the established window ACL contract).
				referenceRunStep(t, p.ctx, c, b.recorder, s)
			}
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("RESET SESSION AUTHORIZATION", 0))
		queryWindowRoutes(t, p, b, referenceSelect("SELECT v FROM range_secret", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
