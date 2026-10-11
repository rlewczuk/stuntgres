package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeMakeNamedDefaults(t *testing.T) {
	datetimeMakeTextCases(t, []datetimeMakeCase{
		{"days", "make_interval(days=>10)", "10 days"},
		{"reorderedAssignment", "make_interval(secs:=1.5,years:=2,days:=-3)", "2 years -3 days +00:00:01.5"},
		{"reorderedClock", "make_interval(mins=>2,hours=>1)", "01:02:00"},
		{"mixed", "make_interval(1,days=>2)", "1 year 2 days"},
		{"months", "make_interval(months=>2)", "2 mons"}, {"weeks", "make_interval(weeks=>2)", "14 days"},
		{"hours", "make_interval(hours=>2)", "02:00:00"}, {"mins", "make_interval(mins=>2)", "00:02:00"}, {"secs", "make_interval(secs=>2)", "00:00:02"},
		{"quotedLabel", "make_interval(\"days\"=>2)", "2 days"},
		{"dateNamed", "make_date(day=>15,year=>2013,month=>7)", "2013-07-15"},
		{"timeNamed", "make_time(sec=>23.5,hour=>8,min=>15)", "08:15:23.5"},
		{"timestampNamed", "make_timestamp(sec=>23.5,mday=>15,year=>2013,hour=>8,month=>7,min=>15)", "2013-07-15 08:15:23.5"},
		{"timestamptzNamed", "make_timestamptz(timezone=>'UTC',sec=>23.5,mday=>15,year=>2013,hour=>8,month=>7,min=>15)", "2013-07-15 08:15:23.5+00"},
		{"qualified", "pg_catalog.make_date(2013,7,15)", "2013-07-15"},
		{"folded", "MAKE_DATE(2013,7,15)", "2013-07-15"},
		{"quotedFunction", "\"make_date\"(2013,7,15)", "2013-07-15"},
		{"unknownInputs", "make_date('2013','7','15')", "2013-07-15"},
	})
	datetimeMakeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN dt_make_int AS integer", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT make_date(2013::dt_make_int,7,15)::text AS result", []string{"result"}, []string{"TEXT"}, [][]any{{"2013-07-15"}}))
		s := fixture.Prepared(fixture.Select("SELECT make_interval(days=>$1,secs=>$2) AS result", []string{"result"}, []string{"INTERVAL"}, [][]any{{"2 days 00:00:01.5"}}, int64(2), 1.5))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	})
}

func TestReferenceDatetimeMakeSignatures(t *testing.T) {
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	cases := []datetimeMakeFailure{
		{"dateMissing", "make_date(2013,7)", "42883", "function make_date(integer, integer) does not exist", hint, 8, true},
		{"dateExtra", "make_date(2013,7,15,1)", "42883", "function make_date(integer, integer, integer, integer) does not exist", hint, 8, true},
		{"timeMissing", "make_time(8,15)", "42883", "function make_time(integer, integer) does not exist", hint, 8, true},
		{"timestampMissing", "make_timestamp(2013,7,15,8,15)", "42883", "function make_timestamp(integer, integer, integer, integer, integer) does not exist", hint, 8, true},
		{"zoneExtra", "make_timestamptz(2013,7,15,8,15,0,'UTC','UTC')", "42883", "function make_timestamptz(integer, integer, integer, integer, integer, integer, unknown, unknown) does not exist", hint, 8, true},
		{"intervalExtra", "make_interval(1,2,3,4,5,6,7,8)", "42883", "function make_interval(integer, integer, integer, integer, integer, integer, integer, integer) does not exist", hint, 8, true},
		{"unknownLabel", "make_interval(day=>1)", "42883", "function make_interval(day => integer) does not exist", hint, 8, true},
		{"quotedUpperLabel", "make_interval(\"DAYS\"=>1)", "42883", "function make_interval(DAYS => integer) does not exist", hint, 8, true},
		{"int8", "make_date(2013::bigint,7,15)", "42883", "function make_date(bigint, integer, integer) does not exist", hint, 8, true},
		{"numeric", "make_date(2013::numeric,7,15)", "42883", "function make_date(numeric, integer, integer) does not exist", hint, 8, true},
		{"textSecs", "make_interval(secs=>'bad'::text)", "42883", "function make_interval(secs => text) does not exist", hint, 8, true},
		{"quotedFunctionUpper", "\"MAKE_DATE\"(2013,7,15)", "42883", "function MAKE_DATE(integer, integer, integer) does not exist", hint, 8, true},
		{"duplicate", "make_interval(days=>1,days=>2)", "42601", "argument name \"days\" used more than once", "", 30, true},
		{"positionalAfterNamed", "make_interval(days=>1,2)", "42601", "positional argument cannot follow named argument", "", 30, true},
	}
	datetimeMakeFailures(t, cases)
}

func TestReferenceDatetimeMakeNullDemand(t *testing.T) {
	datetimeMakeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		expressions := []struct{ sql, typ string }{
			{"make_date(NULL,1,1)", "DATE"}, {"make_date(2000,NULL,1)", "DATE"}, {"make_date(2000,1,NULL)", "DATE"},
			{"make_time(NULL,0,0)", "TIME"}, {"make_time(0,NULL,0)", "TIME"}, {"make_time(0,0,NULL)", "TIME"},
			{"make_timestamptz(2000,1,1,0,0,0,NULL)", "TIMESTAMPTZ"},
			{"make_timestamptz(NULL,1,1,0,0,0,'Nehwon/Lankhmar')", "TIMESTAMPTZ"},
			{"make_interval(years=>NULL)", "INTERVAL"}, {"make_interval(months=>NULL)", "INTERVAL"}, {"make_interval(weeks=>NULL)", "INTERVAL"}, {"make_interval(days=>NULL)", "INTERVAL"}, {"make_interval(hours=>NULL)", "INTERVAL"}, {"make_interval(mins=>NULL)", "INTERVAL"}, {"make_interval(secs=>NULL)", "INTERVAL"},
			{"CASE WHEN false THEN make_date(2013,2,29) ELSE NULL::date END", "DATE"},
		}
		for _, tc := range expressions {
			t.Run(tc.sql, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT "+tc.sql+" AS result", []string{"result"}, []string{tc.typ}, [][]any{{nil}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE dt_make_null(n integer,s text)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dt_make_null VALUES(NULL,'bad')", 1))
		d := datetimeMakeError("22P02", `invalid input syntax for type integer: "bad"`)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT make_interval(days=>n,secs=>s::integer) FROM dt_make_null", d)
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			// Retain the independently observed double-precision input diagnostic
			// through the existing inferred-parameter input seam.
			parameterError := datetimeMakeError("22P02", `invalid input syntax for type double precision: "bad"`)
			parameterError.Context = "unnamed portal parameter $1 = '...'"
			s = fixture.ErrorDiagnostic("SELECT make_interval(days=>n,secs=>$1) FROM dt_make_null", parameterError)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			s.Args = []any{"bad"}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
