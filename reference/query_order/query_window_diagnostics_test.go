package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowDiagnostics(t *testing.T) {
	queryWindowScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowSeed(t, p, b)
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{ q, code, message, marker, hint string }{
			{"SELECT row_number() FROM t", "42809", "window function row_number requires an OVER clause", "row_number", ""},
			{"SELECT row_number() OVER missing FROM t", "42704", `window "missing" does not exist`, "missing", ""},
			{"SELECT row_number() OVER (missing) FROM t", "42704", `window "missing" does not exist`, "(missing)", ""},
			{"SELECT id FROM t WINDOW w AS (), w AS ()", "42P20", `window "w" is already defined`, "()", ""},
			{"SELECT id FROM t WINDOW w AS (later), later AS ()", "42704", `window "later" does not exist`, "(later)", ""},
			{"SELECT id FROM t WINDOW w AS (w)", "42704", `window "w" does not exist`, "(w)", ""},
			{"SELECT row_number() OVER (w PARTITION BY p) FROM t WINDOW w AS ()", "42P20", `cannot override PARTITION BY clause of window "w"`, "(w PARTITION", ""},
			{"SELECT row_number() OVER (w ORDER BY id) FROM t WINDOW w AS (ORDER BY id)", "42P20", `cannot override ORDER BY clause of window "w"`, "(w ORDER", ""},
			{"SELECT id FROM t WHERE row_number() OVER ()>0", "42P20", "window functions are not allowed in WHERE", "row_number", ""},
			{"SELECT count(row_number() OVER ()) OVER () FROM t", "42P20", "window function calls cannot be nested", "row_number", ""},
			{"SELECT count(row_number() OVER ()) FROM t", "42803", "aggregate function calls cannot contain window function calls", "row_number", ""},
			{"SELECT row_number() OVER (ORDER BY row_number() OVER ()) FROM t", "42P20", "window functions are not allowed in window definitions", "row_number", ""},
			{"SELECT row_number() OVER (PARTITION BY row_number() OVER ()) FROM t", "42P20", "window functions are not allowed in window definitions", "row_number", ""},
			{"SELECT count() OVER () FROM t", "42809", "count(*) must be used to call a parameterless aggregate function", "count", ""},
			{"SELECT count(DISTINCT id) OVER () FROM t", "0A000", "DISTINCT is not implemented for window functions", "count", ""},
			{"SELECT count(id ORDER BY id) OVER () FROM t", "0A000", "aggregate ORDER BY is not implemented for window functions", "count", ""},
			{"SELECT row_number() FILTER(WHERE TRUE) OVER () FROM t", "0A000", "FILTER is not implemented for non-aggregate window functions", "row_number", ""},
			{"SELECT row_number(1) OVER () FROM t", "42883", "function row_number(integer) does not exist", "row_number", hint},
			{"SELECT id AS x,row_number() OVER (ORDER BY x) FROM t", "42703", `column "x" does not exist`, "x)", ""},
			{"SELECT row_number() OVER (ORDER BY k),count(*) FROM t", "42803", `column "t.k" must appear in the GROUP BY clause or be used in an aggregate function`, "k)", ""},
			{"SELECT row_number() OVER (ORDER BY setseed(0)) FROM t", "42883", "could not identify an ordering operator for type void", "setseed", "Use an explicit ordering operator or modify the query."},
			{"SELECT row_number() OVER () FROM t FOR UPDATE", "0A000", "FOR UPDATE is not allowed with window functions", "", ""},
			{"SELECT row_number() OVER () FROM t GROUP BY 1", "42P20", "window functions are not allowed in GROUP BY", "row_number", ""},
			{"SELECT id FROM t GROUP BY row_number() OVER ()", "42P20", "window functions are not allowed in GROUP BY", "row_number", ""},
			{"SELECT count(*) FROM t HAVING row_number() OVER ()>0", "42P20", "window functions are not allowed in HAVING", "row_number", ""},
			{"SELECT count(*) FILTER(WHERE row_number() OVER ()>0) FROM t", "42P20", "window functions are not allowed in FILTER", "row_number", ""},
			{"SELECT id FROM t LIMIT row_number() OVER ()", "42P20", "window functions are not allowed in LIMIT", "row_number", ""},
			{"SELECT id FROM t OFFSET row_number() OVER ()", "42P20", "window functions are not allowed in OFFSET", "row_number", ""},
			{"SELECT id FROM t WINDOW w AS (PARTITION BY missing_p ORDER BY missing_k)", "42703", `column "missing_k" does not exist`, "missing_k", ""},
			{"SELECT id FROM t WINDOW w AS (missing ORDER BY missing_k)", "42704", `window "missing" does not exist`, "(missing", ""},
			{"SELECT id FROM t WINDOW w AS (), w AS (ORDER BY missing_k)", "42P20", `window "w" is already defined`, "(ORDER", ""},
			{"SELECT row_number() OVER (w PARTITION BY p) FROM t WINDOW w AS (PARTITION BY p)", "42P20", `cannot override PARTITION BY clause of window "w"`, "(w PARTITION", ""},
			{"SELECT row_number() OVER (ORDER BY missing) FROM empty_t", "42703", `column "missing" does not exist`, "missing", ""},
			{"SELECT row_number() OVER (ORDER BY id) FROM t AS a JOIN t AS b ON a.id=b.id", "42702", `column reference "id" is ambiguous`, "id)", ""},
			{"SELECT CASE WHEN FALSE THEN row_number() ELSE 0 END FROM empty_t", "42809", "window function row_number requires an OVER clause", "row_number", ""},
			{"SELECT row_number(missing) OVER () FROM t", "42703", `column "missing" does not exist`, "missing", ""},
			{"SELECT row_number() OVER w FROM t WHERE EXISTS(SELECT row_number() OVER w) WINDOW w AS ()", "42704", `window "w" does not exist`, "w)", ""},
		} {
			position := 0
			if tc.marker != "" {
				position = strings.LastIndex(tc.q, tc.marker) + 1
			}
			for _, prefix := range []string{"", "/*é*/ "} {
				pos := position
				if pos != 0 {
					pos += len([]rune(prefix))
				}
				s := fixture.ImplicitError(prefix+tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.Position(pos), Hint: tc.hint})
				s.SelectRows = true
				queryWindowRoutes(t, p, b, s)
			}
		}
	})
}

func TestReferenceQueryWindowCheckDiagnostics(t *testing.T) {
	queryWindowScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ q, message, marker string }{
			{"CREATE TABLE t(a INT CHECK(row_number() OVER ()>0))", "window functions are not allowed in check constraints", "row_number"},
			{"CREATE TABLE t(a BIGINT DEFAULT row_number() OVER ())", "window functions are not allowed in DEFAULT expressions", "row_number"},
			{"CREATE TABLE t(a BIGINT GENERATED ALWAYS AS (row_number() OVER ()) STORED)", "window functions are not allowed in column generation expressions", "row_number"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42P20", Message: tc.message, Position: fixture.Position(strings.Index(tc.q, tc.marker) + 1)})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				queryWindowRun(t, p, b, s)
			}
			queryWindowRun(t, p, b, fixture.ImplicitError("SELECT * FROM t", fixture.Diagnostic{Code: "42P01", Message: `relation "t" does not exist`, Position: "15"}))
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE t(a INT)", 0))
		q := "CREATE INDEX bad ON t((row_number() OVER ()))"
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P20", Message: "window functions are not allowed in index expressions", Position: fixture.Position(strings.Index(q, "row_number") + 1)})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			queryWindowRun(t, p, b, s)
		}
	})
}

func TestReferenceQueryWindowReturningDiagnostics(t *testing.T) {
	queryWindowScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE t(id INT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO t VALUES(1)", 1))
		s := fixture.ImplicitError("DELETE FROM t RETURNING row_number() OVER ()", fixture.Diagnostic{Code: "42P20", Message: "window functions are not allowed in RETURNING", Position: "25"})
		s.SelectRows = true
		queryWindowRoutes(t, p, b, s)
		queryWindowRun(t, p, b, fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}

func TestReferenceQueryWindowScalarOverDiagnostics(t *testing.T) {
	queryWindowScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowSeed(t, p, b)
		s := fixture.ImplicitError("SELECT num_nulls(1) OVER () FROM t", fixture.Diagnostic{Code: "42809", Message: "OVER specified, but num_nulls is not a window function nor an aggregate function", Position: "8"})
		s.SelectRows = true
		queryWindowRoutes(t, p, b, s)
	})
}
