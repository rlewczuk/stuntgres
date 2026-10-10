package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"
)

const datetimeZoneCastHint = "No function matches the given name and argument types. You might need to add explicit type casts."

func datetimeZoneDiagnostics(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	for _, tc := range []struct{ expr, signature, anchor string }{
		{"timezone()", "timezone()", "timezone"},
		{"timezone(1)", "timezone(integer)", "timezone"},
		{"timezone('UTC',1)", "timezone(unknown, integer)", "timezone"},
		{"timezone(1,TIMESTAMP '2004-01-01')", "timezone(integer, timestamp without time zone)", "timezone"},
		{"timezone(TRUE,TIMETZ '12:00+00')", "timezone(boolean, time with time zone)", "timezone"},
		{"timezone('UTC','2004-01-01'::TEXT)", "timezone(unknown, text)", "timezone"},
		{"timezone('UTC',TIMESTAMP '2004-01-01',1)", "timezone(unknown, timestamp without time zone, integer)", "timezone"},
		{"date_add()", "date_add()", "date_add"},
		{"date_add(TIMESTAMPTZ '2004-01-01+00')", "date_add(timestamp with time zone)", "date_add"},
		{"date_add(TIMESTAMPTZ '2004-01-01+00',INTERVAL '0',1)", "date_add(timestamp with time zone, interval, integer)", "date_add"},
		{"date_add(TIMESTAMPTZ '2004-01-01+00',INTERVAL '0',INTERVAL '1 hour')", "date_add(timestamp with time zone, interval, interval)", "date_add"},
		{"date_add(TIMESTAMPTZ '2004-01-01+00',INTERVAL '0','UTC',1)", "date_add(timestamp with time zone, interval, unknown, integer)", "date_add"},
		{"date_subtract()", "date_subtract()", "date_subtract"},
		{"date_subtract(TIMESTAMPTZ '2004-01-01+00')", "date_subtract(timestamp with time zone)", "date_subtract"},
		{"date_subtract(TIMESTAMPTZ '2004-01-01+00',INTERVAL '0',TRUE)", "date_subtract(timestamp with time zone, interval, boolean)", "date_subtract"},
		{"date_subtract(TIMESTAMPTZ '2004-01-01+00',INTERVAL '0','UTC',1)", "date_subtract(timestamp with time zone, interval, unknown, integer)", "date_subtract"},
		{"TIMESTAMP '2004-01-01' AT TIME ZONE 1", "pg_catalog.timezone(integer, timestamp without time zone)", "AT"},
		{"1 AT LOCAL", "pg_catalog.timezone(integer)", ""},
		{"\"TimeZone\"(TIMESTAMP '2004-01-01')", "TimeZone(timestamp without time zone)", "\"TimeZone\""},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			q := "SELECT /* é */ " + tc.expr + " FROM tz_witness"
			position := ""
			if tc.anchor != "" {
				position = referencePosition(utf8.RuneCountInString(q[:strings.Index(q, tc.anchor)]) + 1)
			}
			s := referenceImplicitError(q, referenceDiagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: datetimeZoneCastHint, Position: position})
			s.selectRows = true
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, c, b.recorder, s)
			s.prepared, s.errorStage = true, "prepare"
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		})
	}
	for _, tc := range []struct {
		expr          string
		args          []any
		code, message string
	}{
		{"timezone($1::TEXT,TIMESTAMP '2004-01-01')", []any{"No/Such_Zone"}, "22023", `time zone "No/Such_Zone" not recognized`},
		{"timezone($1::INTERVAL,TIMESTAMP '2004-01-01')", []any{"1 month"}, "22023", `interval time zone "1 mon" must not include months or days`},
		{"timezone($1::INTERVAL,TIMESTAMPTZ '2004-01-01+00')", []any{"1 day"}, "22023", `interval time zone "1 day" must not include months or days`},
		{"timezone($1::INTERVAL,TIMETZ '12:00+00')", []any{"infinity"}, "22023", `interval time zone "infinity" must be finite`},
		{"date_add(TIMESTAMPTZ 'infinity',INTERVAL '0',$1::TEXT)", []any{"No/Such_Zone"}, "22023", `time zone "No/Such_Zone" not recognized`},
		{"date_subtract(TIMESTAMPTZ 'infinity',INTERVAL '0',$1::TEXT)", []any{"No/Such_Zone"}, "22023", `time zone "No/Such_Zone" not recognized`},
		{"date_add($1::TIMESTAMPTZ,INTERVAL 'infinity','UTC')", []any{"-infinity"}, "22008", "timestamp out of range"},
		{"date_subtract($1::TIMESTAMPTZ,INTERVAL 'infinity','UTC')", []any{"infinity"}, "22008", "timestamp out of range"},
		{"date_add($1::TIMESTAMPTZ,INTERVAL '1 microsecond','UTC')", []any{"294276-12-31 23:59:59.999999+00"}, "22008", "timestamp out of range"},
		{"date_subtract($1::TIMESTAMPTZ,INTERVAL '1 microsecond','UTC')", []any{"4714-11-24 00:00:00+00 BC"}, "22008", "timestamp out of range"},
		{"date_subtract(TIMESTAMPTZ 'infinity',$1::INTERVAL,'UTC')", []any{"-2147483648 months"}, "22008", "interval out of range"},
		{"date_subtract(TIMESTAMPTZ 'infinity',$1::INTERVAL,'UTC')", []any{"-2147483648 days"}, "22008", "interval out of range"},
		{"date_subtract(TIMESTAMPTZ 'infinity',$1::INTERVAL,'UTC')", []any{"-9223372036854775808 microseconds"}, "22008", "interval out of range"},
	} {
		t.Run(tc.expr+"_"+tc.message, func(t *testing.T) {
			q := "SELECT " + tc.expr + " AS v FROM tz_witness WHERE id=1"
			h := timezoneHeld(t, p, c, q)
			s := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message}, tc.args...)
			s.selectRows, s.errorStage = true, "execute"
			referenceRunStep(t, p.ctx, c, b.recorder, s)
			referenceRunStep(t, p.ctx, h, b.recorder, s)
			datetimeZoneText(t, p, b, c, "timezone('UTC',TIMESTAMP '2004-01-01')", "2004-01-01 00:00:00+00")
		})
	}
	for _, tc := range []struct{ style, rendered string }{
		{"postgres", "1 mon"}, {"postgres_verbose", "@ 1 mon"}, {"sql_standard", "0-1"}, {"iso_8601", "P1M"},
	} {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET IntervalStyle='"+tc.style+"'", 0))
		q := "SELECT timezone($1::INTERVAL,TIMESTAMP '2004-01-01') AS v FROM tz_witness"
		s := referenceImplicitError(q, referenceDiagnostic{Code: "22023", Message: `interval time zone "` + tc.rendered + `" must not include months or days`}, "1 month")
		s.selectRows, s.errorStage = true, "execute"
		datetimeArithmeticBoth(t, p, b, c, s)
	}
}

func TestReferenceDatetimeZoneResolutionAndDiagnostics(t *testing.T) {
	datetimeZoneScenario(t, datetimeZoneDiagnostics)
}
