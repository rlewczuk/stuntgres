package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeTruncDiagnostics(t *testing.T) {
	datetimeTruncScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, source := range []struct{ expr, name string }{{"TIMESTAMP '2001-01-01'", "timestamp without time zone"}, {"TIMESTAMPTZ '2001-01-01+00'", "timestamp with time zone"}, {"INTERVAL '1 day'", "interval"}} {
			for _, field := range []string{"BOGUS", "mm", "epoch", "dow", "doy", "isodow", "isoyear", "j", "jd", "julian", "ago", "@", "quarters", "q", " day", strings.Repeat("É", 40)} {
				normalized := strings.ToLower(field)
				if field == strings.Repeat("É", 40) {
					normalized = strings.Repeat("É", 31)
				}
				d := datetimeExtractFieldDiagnostic("22023", normalized, source.name)
				t.Run(field+source.name, func(t *testing.T) {
					datetimeExtractFailure(t, p, b, c, "SELECT date_trunc('"+field+"',"+source.expr+")", d, "execute")
				})
			}
			want := any(datetimeTruncInstant("2001-01-01T00:00:00Z"))
			typ := "TIMESTAMP"
			if source.name == "timestamp with time zone" {
				typ = "TIMESTAMPTZ"
			}
			if source.name == "interval" {
				typ, want = "INTERVAL", "00:00:00"
			}
			datetimeTruncValue(t, p, b, c, "date_trunc('millenniums',"+source.expr+")", typ, want)
			for _, field := range []string{"timezone", "timezone_hour", "timezone_minute"} {
				datetimeExtractFailure(t, p, b, c, "SELECT date_trunc('"+field+"',"+source.expr+")", datetimeExtractFieldDiagnostic("0A000", field, source.name), "execute")
			}
		}
		for _, value := range []string{"1 day", "infinity", "-infinity"} {
			d := datetimeExtractFieldDiagnostic("0A000", "week", "interval")
			d.Detail = "Months usually have fractional weeks."
			datetimeExtractFailure(t, p, b, c, "SELECT date_trunc('week',INTERVAL '"+value+"')", d, "execute")
		}
		for _, zone := range []string{"Not/AZone", strings.Repeat("x", 300), strings.Repeat("é", 140)} {
			clipped := zone
			if len(clipped) > 255 {
				clipped = clipped[:255]
				for !strings.HasSuffix(clipped, "é") && strings.HasPrefix(zone, "é") {
					clipped = clipped[:len(clipped)-1]
				}
			}
			d := fixture.ControlsReferenceDiagnostic("22023", `time zone "`+clipped+`" not recognized`)
			for _, source := range []string{"2001-01-01+00", "infinity"} {
				datetimeExtractFailure(t, p, b, c, "SELECT date_trunc('bad',TIMESTAMPTZ '"+source+"',$1::text)", d, "execute", zone)
			}
		}
		missing := "No function matches the given name and argument types. You might need to add explicit type casts."
		ambiguous := "Could not choose a best candidate function. You might need to add explicit type casts."
		for _, tc := range []struct{ q, signature, code, hint, position string }{
			{"SELECT date_trunc('day',1)", "date_trunc(unknown, integer)", "42883", missing, "8"},
			{"SELECT date_trunc('day',true)", "date_trunc(unknown, boolean)", "42883", missing, "8"},
			{"SELECT date_trunc('day',NULL::text)", "date_trunc(unknown, text)", "42883", missing, "8"},
			{"SELECT date_trunc('day',TIME WITH TIME ZONE '12:00+00')", "date_trunc(unknown, time with time zone)", "42883", missing, "8"},
			{"SELECT date_trunc('day',INTERVAL '1 day','UTC')", "date_trunc(unknown, interval, unknown)", "42883", missing, "8"},
			{"SELECT date_trunc(1,TIMESTAMP '2001-01-01')", "date_trunc(integer, timestamp without time zone)", "42883", missing, "8"},
			{"SELECT date_trunc('day',TIMESTAMPTZ '2001-01-01+00',1)", "date_trunc(unknown, timestamp with time zone, integer)", "42883", missing, "8"},
			{"SELECT date_trunc()", "date_trunc()", "42883", missing, "8"}, {"SELECT date_trunc('day')", "date_trunc(unknown)", "42883", missing, "8"},
			{"SELECT date_trunc('day',NULL)", "date_trunc(unknown, unknown)", "42725", ambiguous, "8"},
			{"SELECT date_trunc('day','2001-01-01')", "date_trunc(unknown, unknown)", "42725", ambiguous, "8"},
			{"SELECT date_trunc('day',$1)", "date_trunc(unknown, unknown)", "42725", ambiguous, "8"},
			{"/*é*/ SELECT date_trunc('day',1)", "date_trunc(unknown, integer)", "42883", missing, "14"},
			{"SELECT pg_catalog.date_trunc('day',1)", "pg_catalog.date_trunc(unknown, integer)", "42883", missing, "8"},
			{"SELECT \"Date_Trunc\"('day',TIMESTAMP '2001-01-01')", "Date_Trunc(unknown, timestamp without time zone)", "42883", missing, "8"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				verb := " does not exist"
				if tc.code == "42725" {
					verb = " is not unique"
				}
				d := fixture.ControlsReferenceDiagnostic(tc.code, "function "+tc.signature+verb)
				d.Hint, d.Position = tc.hint, tc.position
				var args []any
				if strings.Contains(tc.q, "$1") {
					args = []any{nil}
				}
				datetimeExtractFailure(t, p, b, c, tc.q, d, "prepare", args...)
			})
		}
		q := "SELECT date_trunc(NULL::text,TIMESTAMP 'nonsense')"
		d := fixture.ControlsReferenceDiagnostic("22007", `invalid input syntax for type timestamp: "nonsense"`)
		d.Position = "40"
		datetimeExtractFailure(t, p, b, c, q, d, "prepare")
		datetimeExtractFailure(t, p, b, c, "SELECT date_trunc('millennium',TIMESTAMP '4713-01-01 BC')", fixture.ControlsReferenceDiagnostic("22008", "timestamp out of range"), "execute")
	})
}

func datetimeTruncHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT date_trunc($1::text,$2::timestamptz,$3::text) AT TIME ZONE 'UTC' AS result"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	x := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	good := fixture.Select(q, []string{"result"}, []string{"TIMESTAMP"}, [][]any{{datetimeTruncInstant("2024-03-10T05:00:00Z")}}, "day", "2024-03-10 12:00+00", "America/New_York")
	fixture.RunStep(t, p.Ctx, x, b.Recorder, good)
	for _, tc := range []struct {
		field, source, zone any
		d                   fixture.Diagnostic
	}{
		{"bad", "2024-03-10 12:00+00", "UTC", datetimeExtractFieldDiagnostic("22023", "bad", "timestamp with time zone")},
		{"bad", "infinity", "Not/AZone", fixture.ControlsReferenceDiagnostic("22023", `time zone "Not/AZone" not recognized`)},
		{nil, "nonsense", "UTC", fixture.ControlsReferenceDiagnostic("22007", `invalid input syntax for type timestamp with time zone: "nonsense"`)},
	} {
		d := tc.d
		if tc.source == "nonsense" {
			d.Context = "unnamed portal parameter $2 = '...'"
		}
		s := fixture.ErrorDiagnostic(q, d, tc.field, tc.source, tc.zone)
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, x, b.Recorder, good)
	}
	for _, args := range [][]any{{nil, "2024-03-10 12:00+00", "bad"}, {"bad", nil, "bad"}, {"bad", "2024-03-10 12:00+00", nil}} {
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q, []string{"result"}, []string{"TIMESTAMP"}, [][]any{{nil}}, args...))
	}
	q2 := "SELECT date_trunc($1::text,TIMESTAMP '2001-01-01'+INTERVAL '1 hour'/$2::integer) AS result"
	stmt2, err := c.PrepareContext(p.Ctx, q2)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt2.Close()
	x2 := fixture.HeldStatement{Executor: c, Statement: stmt2, SQL: q2}
	s := fixture.ErrorDiagnostic(q2, fixture.ControlsReferenceDiagnostic("22012", "division by zero"), nil, 0)
	s.SelectRows, s.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, x2, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, x2, b.Recorder, fixture.Select(q2, []string{"result"}, []string{"TIMESTAMP"}, [][]any{{nil}}, nil, 1))
}

func TestReferenceDatetimeTruncHeldStrictRecovery(t *testing.T) {
	datetimeTruncScenario(t, datetimeTruncHeld)
}
