package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations follow REL_18_0 DecodeInterval's field-mask check and
// check_timezone's direct interval_in call (not a GUC-hook rejection).
func TestReferenceTimezoneReviewIntervalDiagnosticsAndAtomicity(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	unchanged := fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"America/New_York"}})
	typed := fixture.Select("SELECT ('2004-07-01 12:00'::TEXT::TIMESTAMPTZ AT TIME ZONE 'UTC')::TEXT AS witness FROM tz_witness", []string{"witness"}, []string{"TEXT"}, [][]any{{"2004-07-01 16:00:00"}})
	for _, input := range []string{"@", "@ ", "@ ago", "@  ago", "ago", "nonsense", "9223372036854775808 microseconds"} {
		code, message := "22007", "invalid input syntax for type interval: \""+input+"\""
		if strings.HasPrefix(input, "922337") {
			code, message = "22015", "interval field value out of range: \""+input+"\""
		}
		for _, q := range []string{
			"SET TIME ZONE INTERVAL '" + input + "'",
			"SET timezone='INTERVAL ''" + input + "'''",
			"SELECT set_config('timezone','INTERVAL ''" + input + "''',false)",
		} {
			t.Run(q, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					timezoneBoth(t, p, fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
					s.Prepared, s.SelectRows, s.ErrorStage = prepared, strings.HasPrefix(q, "SELECT"), "execute"
					p.Step(t, s)
					timezoneBoth(t, p, unchanged)
					timezoneBoth(t, p, typed)
				}
			})
		}
	}
	for _, tc := range []struct{ value, detail string }{
		{"No/Such_Zone", ""},
		{"INTERVAL nonsense", ""},
		{"INTERVAL '1 month'", "Cannot specify months in time zone interval."},
		{"INTERVAL '1 day'", "Cannot specify days in time zone interval."},
		{"INTERVAL '168 hours'", "UTC timezone offset is out of range."},
	} {
		for _, prepared := range []bool{false, true} {
			timezoneBoth(t, p, fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
			s := fixture.ImplicitError("SELECT set_config('timezone',$1,false)", fixture.Diagnostic{Code: "22023", Message: "invalid value for parameter \"TimeZone\": \"" + tc.value + "\"", Detail: tc.detail}, tc.value)
			s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
			p.Step(t, s)
			timezoneBoth(t, p, unchanged)
			timezoneBoth(t, p, typed)
		}
	}
	for _, input := range []string{"0 seconds", "@ 0 seconds", "@ 0 seconds ago"} {
		timezoneBoth(t, p, fixture.Exec("SET TIME ZONE INTERVAL '"+input+"'", 0))
		timezoneBoth(t, p, fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"<+00>-00"}}))
	}
}
