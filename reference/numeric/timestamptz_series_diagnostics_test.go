package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTimestamptzSeriesDiagnostics(t *testing.T) {
	timestamptzSeriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ start, stop, step, zone, code, message string }{
			{"2024-01-01+00", "2024-01-02+00", "0 days", "UTC", "22023", "step size cannot equal zero"},
			{"2024-01-02+00", "2024-01-01+00", "1 month -30 days", "UTC", "22023", "step size cannot equal zero"},
			{"2024-01-01+00", "2024-01-02+00", "1 day -24 hours", "UTC", "22023", "step size cannot equal zero"},
			{"2024-01-01+00", "2024-01-02+00", "infinity", "UTC", "22023", "step size cannot be infinite"},
			{"2024-01-02+00", "2024-01-01+00", "-infinity", "UTC", "22023", "step size cannot be infinite"},
			{"2024-01-01+00", "2024-01-02+00", "1 day", "No/Such_Zone", "22023", `time zone "No/Such_Zone" not recognized`},
			{"2024-01-02+00", "2024-01-01+00", "0 days", "No/Such_Zone", "22023", `time zone "No/Such_Zone" not recognized`},
			{"infinity", "infinity", "infinity", "No/Such_Zone", "22023", `time zone "No/Such_Zone" not recognized`},
			{"294276-12-31 23:59:59.999999+00", "294276-12-31 23:59:59.999999+00", "1 microsecond", "UTC", "22008", "timestamp out of range"},
			{"4714-11-24 00:00:00+00 BC", "4714-11-24 00:00:00+00 BC", "-1 microsecond", "UTC", "22008", "timestamp out of range"},
			{"294276-12-31 00:00+00", "294276-12-31 00:00+00", "1 month", "UTC", "22008", "timestamp out of range"},
			{"294276-12-31 00:00+00", "294276-12-31 00:00+00", "1 day", "UTC", "22008", "timestamp out of range"},
		} {
			t.Run(tc.start+"/"+tc.step+"/"+tc.zone, func(t *testing.T) {
				for _, suffix := range []string{"", " LIMIT 1"} {
					q := "/*é*/ SELECT generate_series(TIMESTAMPTZ '" + tc.start + "',TIMESTAMPTZ '" + tc.stop + "',INTERVAL '" + tc.step + "','" + tc.zone + "') AS v" + suffix
					s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", tc.code, tc.message, ""))
					s.SelectRows, s.ErrorStage = true, "execute"
					seriesBoth(t, p, b, c, s)
				}
			})
		}
		for _, args := range []string{
			"NULL::timestamptz,TIMESTAMPTZ '2024-01-02+00',INTERVAL '0 days','No/Such_Zone'",
			"TIMESTAMPTZ '2024-01-01+00',NULL::timestamptz,INTERVAL 'infinity','No/Such_Zone'",
			"TIMESTAMPTZ '2024-01-01+00',TIMESTAMPTZ '2024-01-02+00',NULL::interval,'No/Such_Zone'",
			"TIMESTAMPTZ '2024-01-01+00',TIMESTAMPTZ '2024-01-02+00',INTERVAL '0 days',NULL::text",
			"TIMESTAMPTZ '2024-01-01+00',TIMESTAMPTZ '2024-01-02+00',INTERVAL '-infinity',NULL::text",
			"NULL::timestamptz,TIMESTAMPTZ 'infinity',INTERVAL 'infinity'",
			"TIMESTAMPTZ '294276-12-31 23:59:59.999999+00',TIMESTAMPTZ '2024-01-01+00',INTERVAL '1 microsecond','UTC'",
		} {
			timestampSeriesBoth(t, p, b, c, fixture.Select("SELECT generate_series("+args+") AS v", []string{"v"}, []string{"TIMESTAMPTZ"}, nil))
		}
		q := "/*é*/ SELECT generate_series(NULL::timestamptz,TIMESTAMPTZ '2024-01-02+00','oops'::interval,'No/Such_Zone')"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "'oops'", "22007", `invalid input syntax for type interval: "oops"`, ""))
			s.SelectRows, s.Prepared = true, prepared
			s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		// Release lookup copies at most 255 bytes into its series-local zone buffer.
		for _, name := range []string{strings.Repeat("x", 255), strings.Repeat("x", 256), strings.Repeat("x", 255) + "UTC", strings.Repeat("é", 128)} {
			q := "SELECT generate_series(TIMESTAMPTZ '2024-01-01+00',TIMESTAMPTZ '2024-01-02+00',INTERVAL '1 day',$1::text)"
			clipped := name
			if len(clipped) > 255 {
				clipped = clipped[:255]
			}
			if name == strings.Repeat("é", 128) {
				clipped = strings.Repeat("é", 127)
			}
			s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22023", "time zone \""+clipped+"\" not recognized", ""), name)
			s.SelectRows, s.ErrorStage = true, "execute"
			seriesBoth(t, p, b, c, s)
		}
		// A valid full 255-byte POSIX prefix proves clipping precedes lookup.
		for _, name := range []string{strings.Repeat("A", 254) + "0", strings.Repeat("A", 254) + "0BAD"} {
			q := "SELECT generate_series(TIMESTAMPTZ '2024-01-01+00',TIMESTAMPTZ '2024-01-02+00',INTERVAL '1 day',$1::text) AS v ORDER BY v"
			timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TIMESTAMPTZ"}, timestampSeriesRows("2024-01-01 00:00:00", "2024-01-02 00:00:00"), name))
		}
		q = "SELECT generate_series(NULL::timestamptz,TIMESTAMPTZ '2024-01-02+00',$1::interval,'No/Such_Zone')"
		d := seriesDiagnostic(q, "", "22007", `invalid input syntax for type interval: "oops"`, "")
		d.Context = "unnamed portal parameter $1 = '...'"
		s := fixture.ErrorDiagnostic(q, d, "oops")
		s.SelectRows, s.ErrorStage = true, "execute"
		seriesBoth(t, p, b, c, s)
		// PostgreSQL Bind and the native UTF8 text codec reject NUL before
		// provider strictness, with no parameter-value suffix in Context.
		q = "SELECT generate_series(NULL::timestamptz,TIMESTAMPTZ '2024-01-02+00',INTERVAL '0 days',$1::text)"
		d = seriesDiagnostic(q, "", "22021", `invalid byte sequence for encoding "UTF8": 0x00`, "")
		d.Context = "unnamed portal parameter $1"
		s = fixture.ErrorDiagnostic(q, d, "UTC\x00No/Such_Zone")
		s.SelectRows, s.ErrorStage = true, "execute"
		seriesBoth(t, p, b, c, s)
	})
}
