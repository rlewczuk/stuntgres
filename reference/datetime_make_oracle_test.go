package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

// These independent expectations were observed by Main against pinned PG18.0
// in tests-1/artifact25. Catalog-discovery queries are intentionally not retained.
func TestReferenceDatetimeMakeOracleBoundaries(t *testing.T) {
	datetimeMakeTextCases(t, []datetimeMakeCase{
		{"negativeMicrosLimit", "make_interval(secs=>-9223372036854.775808)", "-2562047788:00:54.775808"},
		{"dynamicMSK", "make_timestamptz(2013,7,15,12,0,0,'MSK')", "2013-07-15 08:00:00+00"},
	})
	datetimeMakeFailures(t, []datetimeMakeFailure{
		{name: "minYearDate", expression: "make_date(-2147483648,1,1)", code: "22008", message: "date field value out of range: -2147483648-01-01"},
		{name: "minYearTimestamp", expression: "make_timestamp(-2147483648,1,1,0,0,0)", code: "22008", message: "date field value out of range: -2147483648-01-01"},
		{name: "roundedExclusiveEnd", expression: "make_timestamp(294276,12,31,23,59,59.9999995)", code: "22008", message: "timestamp out of range: 294276-12-31 23:59:60"},
		{name: "clockNaN", expression: "make_time(0,0,'NaN'::float8)", code: "22008", message: "time field value out of range: 0:00:NaN"},
		{name: "clockInfinity", expression: "make_time(0,0,'Infinity'::float8)", code: "22008", message: "time field value out of range: 0:00:Infinity"},
		{name: "clockNegativeInfinity", expression: "make_time(0,0,'-Infinity'::float8)", code: "22008", message: "time field value out of range: 0:00:-Infinity"},
		{name: "NULLConstantChildFold", expression: "make_date(NULL,1,1/0)", code: "22012", message: "division by zero"},
		{name: "NULLInvalidUnknownInput", expression: "make_date(NULL,'bad',1)", code: "22P02", message: `invalid input syntax for type integer: "bad"`, position: 23, analysis: true},
	})
}

func TestReferenceDatetimeMakeOracleSignatures(t *testing.T) {
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	datetimeMakeFailures(t, []datetimeMakeFailure{
		{"manualDayNotLabel", "make_timestamp(day=>15,month=>7,year=>2013,hour=>8,min=>15,sec=>0)", "42883", "function make_timestamp(day => integer, month => integer, year => integer, hour => integer, min => integer, sec => integer) does not exist", hint, 8, true},
		{"positionalNameCollision", "make_interval(1,years=>2)", "42883", "function make_interval(integer, years => integer) does not exist", hint, 8, true},
		{"variadic", "make_interval(VARIADIC '{1,2}'::integer[])", "42883", "function make_interval(integer[]) does not exist", hint, 8, true},
		{"adjacentUnknownNamed", "abs(a=>1)", "42883", "function abs(a => integer) does not exist", hint, 8, true},
		{"adjacentNullsNamed", "num_nulls(a=>1)", "42883", "function num_nulls(a => integer) does not exist", hint, 8, true},
		{"adjacentNonnullsAssignment", "num_nonnulls(a:=1)", "42883", "function num_nonnulls(a => integer) does not exist", hint, 8, true},
		{"UTF8Position", "/* é🙂 */ make_date(2013::bigint,7,15)", "42883", "function make_date(bigint, integer, integer) does not exist", hint, 17, true},
		{"NULLMissing", "make_date(NULL,1)", "42883", "function make_date(unknown, integer) does not exist", hint, 8, true},
		{"hundredArguments", "make_interval(" + strings.TrimSuffix(strings.Repeat("1,", 100), ",") + ")", "42883", "function make_interval(" + strings.TrimSuffix(strings.Repeat("integer, ", 100), ", ") + ") does not exist", hint, 8, true},
		{"hundredOneArguments", "make_interval(" + strings.TrimSuffix(strings.Repeat("1,", 101), ",") + ")", "54023", "cannot pass more than 100 arguments to a function", "", 8, true},
		{"childBeforeLimit", "make_interval(" + strings.Repeat("1,", 100) + "missing_column)", "42703", `column "missing_column" does not exist`, "", 222, true},
	})
}

func TestReferenceDatetimeMakeOracleEvaluationAndDeparse(t *testing.T) {
	datetimeMakeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE dt_make_order(days text,seconds text)", 0))
		run(referenceExec("INSERT INTO dt_make_order VALUES('bad-day','bad-second')", 1))
		for _, q := range []string{
			"SELECT make_interval(secs=>seconds::integer,days=>days::integer) FROM dt_make_order",
			"SELECT make_interval(days=>days::integer,secs=>seconds::integer) FROM dt_make_order",
		} {
			for _, prepared := range []bool{false, true} {
				s := referenceErrorDiagnostic(q, datetimeMakeError("22P02", `invalid input syntax for type integer: "bad-day"`))
				s.selectRows = true
				s.prepared = prepared
				s.errorStage = "execute"
				run(s)
			}
		}
		run(referenceExec("CREATE VIEW dt_make_named_definition AS SELECT make_interval(secs=>'1.5',days=>2) AS iv,make_date(day=>15,year=>2013,month=>7) AS d", 0))
		run(referenceSelect("SELECT pg_get_viewdef('dt_make_named_definition'::regclass,true)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{" SELECT make_interval(secs => '1.5'::double precision, days => 2) AS iv,\n    make_date(day => 15, year => 2013, month => 7) AS d;"}}))
	})
}

// Source-grounded lower carry distinguishes Julian admissibility from the
// final timestamp range. Main validates these supplemental inputs in PG18.0.
func TestReferenceDatetimeMakeLowerCarry(t *testing.T) {
	datetimeMakeTextCases(t, []datetimeMakeCase{
		{"timestampCarry", "make_timestamp(-4714,11,23,24,0,0)", "4714-11-24 00:00:00 BC"},
		{"timestamptzCarry", "make_timestamptz(-4714,11,23,24,0,0,'UTC')", "4714-11-24 00:00:00+00 BC"},
	})
	datetimeMakeFailures(t, []datetimeMakeFailure{
		{name: "timestampMidnight", expression: "make_timestamp(-4714,11,23,0,0,0)", code: "22008", message: "timestamp out of range: -4714-11-23 0:00:00"},
		{name: "timestamptzMidnight", expression: "make_timestamptz(-4714,11,23,0,0,0,'UTC')", code: "22008", message: "timestamp out of range: -4714-11-23 0:00:00"},
	})
}
