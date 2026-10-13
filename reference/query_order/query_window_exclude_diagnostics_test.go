package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations derive from the PG18 grammar/analysis contracts, not native
// output. The test-only oracle checkpoint resolves new token-owner predictions.
func queryWindowExcludeDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, tc := range []struct{ q, code, message, marker, hint string }{
		{"SELECT count(*) OVER(ROWS CURRENT ROW EXCLUDE BAD) FROM ex_t", "42601", `syntax error at or near "BAD"`, "BAD", ""},
		{"SELECT count(*) OVER(ROWS CURRENT ROW EXCLUDE ) FROM ex_t", "42601", `syntax error at or near ")"`, ") FROM", ""},
		{"SELECT count(*) OVER(ORDER BY k EXCLUDE GROUP) FROM ex_t", "42601", `syntax error at or near "EXCLUDE"`, "EXCLUDE", ""},
		{"SELECT count(*) OVER(ROWS CURRENT ROW EXCLUDE GROUP EXCLUDE TIES) FROM ex_t", "42601", `syntax error at or near "EXCLUDE"`, "EXCLUDE TIES", ""},
		{"SELECT count(*) OVER(ROWS missing PRECEDING EXCLUDE GROUP) FROM ex_t", "42703", `column "missing" does not exist`, "missing", ""},
		{"SELECT count(*) OVER(ROWS k PRECEDING EXCLUDE TIES) FROM ex_t", "42P10", "argument of ROWS must not contain variables", "k PRECEDING", ""},
		{"SELECT count(*) OVER(ROWS TRUE PRECEDING EXCLUDE CURRENT ROW) FROM ex_t", "42804", "argument of ROWS must be type bigint, not type boolean", "TRUE", ""},
		{"SELECT count(*) OVER(ROWS UNBOUNDED FOLLOWING EXCLUDE TIES) FROM ex_t", "42P20", "frame start cannot be UNBOUNDED FOLLOWING", "UNBOUNDED", ""},
		{"SELECT count(*) OVER(ROWS BETWEEN CURRENT ROW AND (+1) PRECEDING EXCLUDE GROUP) FROM ex_t", "42P20", "frame starting from current row cannot have preceding rows", "(+1)", ""},
		{"SELECT sum(v) OVER missing FROM ex_t", "42704", `window "missing" does not exist`, "missing", ""},
		{"SELECT sum(v) OVER(w) FROM ex_t WINDOW w AS(ROWS CURRENT ROW EXCLUDE NO OTHERS)", "42P20", `cannot copy window "w" because it has a frame clause`, "(w)", "Omit the parentheses in this OVER clause."},
		{"SELECT sum(NULL) OVER(ROWS CURRENT ROW EXCLUDE GROUP) FROM ex_t", "42725", "function sum(unknown) is not unique", "sum", "Could not choose a best candidate function. You might need to add explicit type casts."},
		{"SELECT sum(TRUE) OVER(ROWS CURRENT ROW EXCLUDE GROUP) FROM ex_t", "42883", "function sum(boolean) does not exist", "sum", "No function matches the given name and argument types. You might need to add explicit type casts."},
		{"SELECT sum(row_number() OVER()) OVER(ROWS CURRENT ROW EXCLUDE GROUP) FROM ex_t", "42P20", "window function calls cannot be nested", "row_number", ""},
		{"SELECT id FROM ex_t WHERE sum(v) OVER(ROWS CURRENT ROW EXCLUDE GROUP)>0", "42P20", "window functions are not allowed in WHERE", "sum", ""},
	} {
		for _, prefix := range []string{"", "/*é🙂*/ "} {
			queryWindowRangeAnalysis(t, p, b, prefix+tc.q, tc.code, tc.message, tc.marker, tc.hint)
		}
	}
}

func TestReferenceQueryWindowExcludeDiagnostics(t *testing.T) {
	queryWindowExcludeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		queryWindowExcludeDiagnostics(t, p, b)
		q := "INSERT INTO ex_t VALUES(4,4,40);SELECT count(*) OVER(ROWS CURRENT ROW EXCLUDE BAD) FROM ex_t"
		queryWindowRangeAnalysis(t, p, b, "SELECT count(*) OVER(ROWS CURRENT ROW EXCLUDE BAD) FROM ex_t", "42601", `syntax error at or near "BAD"`, "BAD", "")
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "BAD"`, Position: fixture.Position(1 + len([]rune("INSERT INTO ex_t VALUES(4,4,40);SELECT count(*) OVER(ROWS CURRENT ROW EXCLUDE ")))})
		s.SelectRows = true
		s.ErrorStage = "execute"
		queryWindowRun(t, p, b, s)
		queryWindowRoutes(t, p, b, fixture.Select("SELECT count(*) AS n FROM ex_t", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
	})
}

func queryWindowExcludePrepared(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	q := queryWindowExcludeQuery("ORDER BY id ROWS BETWEEN $1 PRECEDING AND CURRENT ROW EXCLUDE CURRENT ROW")
	stmt, err := b.DB.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{b.DB, stmt, q}
	for _, arg := range []any{1, nil, -1, 2, 1} {
		s := queryWindowExcludeResult(q, []int64{0, 1, 1}, []any{nil, int64(10), int64(20)})
		if arg == 2 {
			s = queryWindowExcludeResult(q, []int64{0, 1, 2}, []any{nil, int64(10), int64(30)})
		}
		s.Args = []any{arg}
		if arg == nil || arg == -1 {
			code, message := "22004", "frame starting offset must not be null"
			if arg == -1 {
				code, message = "22013", "frame starting offset must not be negative"
			}
			s = fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, arg)
			s.SelectRows = true
			s.ErrorStage = "execute"
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}

func TestReferenceQueryWindowExcludePrepared(t *testing.T) {
	queryWindowExcludeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		queryWindowExcludePrepared(t, p, b)
	})
}

func TestReferenceQueryWindowExcludeDemand(t *testing.T) {
	queryWindowExcludeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		for _, call := range []string{"count(*)", "sum(v)"} {
			for _, suffix := range []string{"", " WHERE FALSE"} {
				for _, tc := range []struct{ bound, code, message string }{
					{"NULL PRECEDING", "22004", "frame starting offset must not be null"},
					{"-1 PRECEDING", "22013", "frame starting offset must not be negative"},
					{"BETWEEN CURRENT ROW AND NULL FOLLOWING", "22004", "frame ending offset must not be null"},
					{"BETWEEN CURRENT ROW AND -1 FOLLOWING", "22013", "frame ending offset must not be negative"},
				} {
					q := "SELECT " + call + " OVER(ORDER BY id ROWS " + tc.bound + " EXCLUDE GROUP) FROM ex_t" + suffix
					queryWindowRangeRuntime(t, p, b, q, tc.code, tc.message)
				}
			}
		}
		queryWindowRoutes(t, p, b, fixture.Select("SELECT sum(v) OVER(ROWS NULL PRECEDING EXCLUDE GROUP) AS s FROM ex_t LIMIT 0", []string{"s"}, []string{"INT8"}, nil))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT row_number() OVER(ORDER BY id ROWS NULL PRECEDING EXCLUDE TIES) AS rn FROM ex_t ORDER BY id", []string{"rn"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id FROM ex_t WINDOW unused AS(ROWS NULL PRECEDING EXCLUDE GROUP) ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
	})
}
