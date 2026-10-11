package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimezoneDiagnosticsParameterAtomicity(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	p.Step(t, fixture.Exec("UPDATE tz_witness SET z='2004-01-01+00',q='12:00+00' WHERE id=1", 1))
	for _, backend := range p.Backends() {
		var q any = "12:00:00+00"
		if backend.PG {
			q = time.Date(0, 1, 1, 12, 0, 0, 0, time.UTC)
		}
		sentinel := fixture.Select("SELECT z::TEXT AS z,q FROM tz_witness", []string{"z", "q"}, []string{"TEXT", "TIMETZ"}, [][]any{{"2004-01-01 00:00:00+00", q}})
		query := "INSERT INTO tz_witness(z) VALUES($1)"
		held := timezoneHeld(t, p, backend.DB, query)
		s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type timestamp with time zone: "nonsense"`, Context: "unnamed portal parameter $1 = '...'"}, "nonsense")
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, sentinel)
		for _, query := range []string{"INSERT INTO tz_witness(z) VALUES($1),($2)", "UPDATE tz_witness SET z=$1 WHERE q=$2"} {
			args := []any{"nonsense", "12:00+00"}
			parameter := "$1"
			if strings.HasPrefix(query, "INSERT") {
				args = []any{"2005-01-01+00", "nonsense"}
				parameter = "$2"
			}
			s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type timestamp with time zone: "nonsense"`, Context: "unnamed portal parameter " + parameter + " = '...'"}, args...)
			s.ErrorStage = "execute"
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, sentinel)
			}
		}
	}
}

func TestReferenceTimezoneDiagnosticsUtilityPhases(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	for _, tc := range []struct{ expression, code, message, position, hint string }{
		{"TIMESTAMPTZ 'nonsense'", "22007", `invalid input syntax for type timestamp with time zone: "nonsense"`, "20", ""},
		{"TIMESTAMPTZ '2004-10-19 10:00+16'", "22009", `time zone displacement out of range: "2004-10-19 10:00+16"`, "20", ""},
		{"TIMESTAMPTZ '2004-10-19 10:00 No/Such_Zone'", "22023", `time zone "no/such_zone" not recognized`, "20", ""},
		{"TIMETZ '10:00 America/New_York'", "22007", `invalid input syntax for type time with time zone: "10:00 America/New_York"`, "15", ""},
		{"DATE '31/12/2024'", "22008", `date/time field value out of range: "31/12/2024"`, "13", `Perhaps you need a different "DateStyle" setting.`},
	} {
		s := fixture.ImplicitError("SELECT "+tc.expression+" FROM tz_witness", fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint})
		s.SelectRows = true
		s.ErrorStage = "execute"
		p.Step(t, s)
		s.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(s))
	}
	// These parser errors were originally exercised through Query, not Exec.
	for _, tc := range []struct{ q, message, position string }{{"SET TIME ZONE = 'UTC'", `syntax error at or near "="`, "15"}, {"SET TIME ZONE INTERVAL '1' DAY", "time zone interval must be HOUR or HOUR TO MINUTE", "28"}} {
		s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42601", Message: tc.message, Position: tc.position})
		s.SelectRows = true
		s.ErrorStage = "execute"
		p.Step(t, s)
		s.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(s))
	}
}

func TestReferenceTimezoneRuntimeZoneAndIntervalDiagnostics(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	p.Step(t, fixture.Exec("UPDATE tz_witness SET c='2004-10-19 10:00' WHERE id=1", 1))
	for _, backend := range p.Backends() {
		query := "SELECT c AT TIME ZONE $1 AS z FROM tz_witness"
		held := timezoneHeld(t, p, backend.DB, query)
		for _, zone := range []string{"No/Such_Zone", "bad/zone"} {
			s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22023", Message: `time zone "` + zone + `" not recognized`}, zone)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT (c AT TIME ZONE $1)::TEXT AS z FROM tz_witness", []string{"z"}, []string{"TEXT"}, [][]any{{"2004-10-19 10:00:00+00"}}, "UTC"))
		for _, tc := range []struct{ q, code, message, detail string }{
			{"SET TIME ZONE INTERVAL '168 hours'", "22023", `invalid value for parameter "TimeZone": "INTERVAL '168:00:00'"`, "UTC timezone offset is out of range."},
			{"SET TIME ZONE INTERVAL 'nonsense'", "22007", `invalid input syntax for type interval: "nonsense"`, ""},
		} {
			held := timezoneHeld(t, p, backend.DB, tc.q)
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"UTC"}}))
		}
	}
}
