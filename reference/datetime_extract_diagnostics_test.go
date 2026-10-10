package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceDatetimeExtractDiagnostics(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, source := range []struct{ expr, diagnostic string }{{"DATE '2000-01-01'", "date"}, {"TIME '00:00'", "time without time zone"}, {"TIMETZ '00:00+00'", "time with time zone"}, {"TIMESTAMP '2000-01-01'", "timestamp without time zone"}, {"TIMESTAMPTZ '2000-01-01+00'", "timestamp with time zone"}, {"INTERVAL '1 day'", "interval"}} {
			for _, api := range []string{"extract", "date_part"} {
				diagnostic := source.diagnostic
				if api == "date_part" && diagnostic == "date" {
					diagnostic = "timestamp without time zone"
				}
				q := "SELECT " + datetimeExtractSQL(api, "BOGUS", source.expr)
				t.Run(q, func(t *testing.T) {
					d := datetimeExtractFieldDiagnostic("22023", "bogus", diagnostic)
					if api == "date_part" && source.diagnostic == "date" {
						d = datetimeExtractDateWrapperDiagnostic(d)
					}
					datetimeExtractFailure(t, p, b, c, q, d, "execute")
				})
			}
		}
		missingHint := "No function matches the given name and argument types. You might need to add explicit type casts."
		ambiguousHint := "Could not choose a best candidate function. You might need to add explicit type casts."
		for _, tc := range []struct {
			q, code, message, hint string
			position               int
		}{
			{"SELECT EXTRACT(year FROM 1)", "42883", "function pg_catalog.extract(unknown, integer) does not exist", missingHint, 8},
			{"SELECT EXTRACT(year FROM true)", "42883", "function pg_catalog.extract(unknown, boolean) does not exist", missingHint, 8},
			{"SELECT EXTRACT(year FROM '2000-01-01'::text)", "42883", "function pg_catalog.extract(unknown, text) does not exist", missingHint, 8},
			{"SELECT date_part('year',1)", "42883", "function date_part(unknown, integer) does not exist", missingHint, 8},
			{"SELECT date_part('year',true)", "42883", "function date_part(unknown, boolean) does not exist", missingHint, 8},
			{"SELECT date_part('year','2000-01-01'::text)", "42883", "function date_part(unknown, text) does not exist", missingHint, 8},
			{"SELECT date_part('year')", "42883", "function date_part(unknown) does not exist", missingHint, 8},
			{"SELECT date_part('year',DATE '2000-01-01',1)", "42883", "function date_part(unknown, date, integer) does not exist", missingHint, 8},
			{"SELECT \"Date_Part\"('year',DATE '2000-01-01')", "42883", "function Date_Part(unknown, date) does not exist", missingHint, 8},
			{"/*é*/ SELECT date_part('year',1)", "42883", "function date_part(unknown, integer) does not exist", missingHint, 14},
			{"SELECT EXTRACT(year FROM NULL)", "42725", "function pg_catalog.extract(unknown, unknown) is not unique", ambiguousHint, 8},
			{"SELECT EXTRACT(year FROM '2000-01-01')", "42725", "function pg_catalog.extract(unknown, unknown) is not unique", ambiguousHint, 8},
			{"SELECT EXTRACT(year FROM $1)", "42725", "function pg_catalog.extract(unknown, unknown) is not unique", ambiguousHint, 8},
			{"SELECT date_part('year',NULL)", "42725", "function date_part(unknown, unknown) is not unique", ambiguousHint, 8},
			{"SELECT date_part('year',$1)", "42725", "function date_part(unknown, unknown) is not unique", ambiguousHint, 8},
			{"SELECT EXTRACT(year DATE '2000-01-01')", "42601", `syntax error at or near "DATE"`, "", 21},
			{"SELECT EXTRACT(year FROM)", "42601", `syntax error at or near ")"`, "", 25},
			{"SELECT EXTRACT(year FROM DATE '2000-01-01'", "42601", "syntax error at end of input", "", 43},
			{"SELECT EXTRACT($1 FROM DATE '2000-01-01')", "42601", `syntax error at or near "$1"`, "", 16},
			{"SELECT EXTRACT(lower('year') FROM DATE '2000-01-01')", "42601", `syntax error at or near "("`, "", 21},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := controlsReferenceDiagnostic(tc.code, tc.message)
				d.Hint, d.Position = tc.hint, referencePosition(tc.position)
				var args []any
				if strings.Contains(tc.q, "$1") {
					args = []any{nil}
				}
				datetimeExtractFailure(t, p, b, c, tc.q, d, "prepare", args...)
			})
		}
	})
}

func datetimeExtractHeld(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	q := "SELECT date_part($1::text,$2::timestamp) AS result"
	stmt, err := c.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	x := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
	for _, tc := range []struct {
		field, source any
		want          any
	}{
		{"year", "2000-02-29 17:12:28.123456", float64(2000)},
		{"second", "2000-02-29 17:12:28.123456", float64(28.123456)},
		{"m", "2000-02-29 17:12:28.123456", float64(12)},
		{nil, "2000-02-29", nil}, {"bad", nil, nil}, {"year", "2001-01-01", float64(2001)},
	} {
		referenceRunStep(t, p.ctx, x, b.recorder, referenceSelect(q, []string{"result"}, []string{"FLOAT8"}, [][]any{{tc.want}}, tc.field, tc.source))
	}
	for _, tc := range []struct {
		field, source any
		diagnostic    referenceDiagnostic
	}{
		{"bad", "2000-01-01", datetimeExtractFieldDiagnostic("22023", "bad", "timestamp without time zone")},
		{nil, "nonsense", controlsReferenceDiagnostic("22007", `invalid input syntax for type timestamp: "nonsense"`)},
	} {
		d := tc.diagnostic
		if tc.source == "nonsense" {
			d.Context = "unnamed portal parameter $2 = '...'"
		}
		s := referenceErrorDiagnostic(q, d, tc.field, tc.source)
		s.selectRows, s.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, x, b.recorder, s)
		referenceRunStep(t, p.ctx, x, b.recorder, referenceSelect(q, []string{"result"}, []string{"FLOAT8"}, [][]any{{float64(2000)}}, "year", "2000-01-01"))
	}
}

func TestReferenceDatetimeExtractStrictChildrenAndHeldRecovery(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		datetimeExtractHeld(t, p, b, c)
		for _, typ := range []string{"date", "time", "timetz", "timestamp", "timestamptz", "interval"} {
			for _, api := range []string{"extract", "date_part"} {
				q := "SELECT " + datetimeExtractSQL(api, "bad", "NULL::"+typ)
				resultType := "NUMERIC"
				if api == "date_part" {
					resultType = "FLOAT8"
				}
				datetimeExtractBoth(t, p, b, c, referenceSelect(q, []string{api}, []string{resultType}, [][]any{{nil}}))
			}
		}
		for _, tc := range []struct{ q, message string }{
			{"SELECT date_part(NULL::text,TIMESTAMP 'nonsense')", `invalid input syntax for type timestamp: "nonsense"`},
			{"/*é*/ SELECT EXTRACT(year FROM DATE 'bad')", `invalid input syntax for type date: "bad"`},
		} {
			d := controlsReferenceDiagnostic("22007", tc.message)
			d.Position = referencePosition(len([]rune(tc.q[:strings.Index(tc.q, "'")])) + 1)
			datetimeExtractFailure(t, p, b, c, tc.q, d, "prepare")
		}
		q := "SELECT date_part($1::text,TIMESTAMP '2000-01-01'+INTERVAL '1 hour'/$2::integer)"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		x := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		s := referenceErrorDiagnostic(q, controlsReferenceDiagnostic("22012", "division by zero"), nil, 0)
		s.selectRows, s.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, x, b.recorder, s)
		referenceRunStep(t, p.ctx, x, b.recorder, referenceSelect(q, []string{"date_part"}, []string{"FLOAT8"}, [][]any{{nil}}, nil, 1))
		// Dynamic text is an expression/column, not a literal-only decoder.
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE dx_fields(f text,s timestamp)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO dx_fields VALUES('YEAR',TIMESTAMP '2000-01-01'),('SECOND',TIMESTAMP '2000-01-01 00:00:28.5')", 2))
		datetimeExtractBoth(t, p, b, c, referenceSelect("SELECT date_part(lower(f),s) AS value FROM dx_fields ORDER BY f", []string{"value"}, []string{"FLOAT8"}, [][]any{{float64(28.5)}, {float64(2000)}}))
	})
}
