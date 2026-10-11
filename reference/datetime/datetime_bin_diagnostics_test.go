package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func datetimeBinDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, typ := range []string{"timestamp", "timestamptz"} {
		for _, tc := range []struct{ stride, source, origin, code, message string }{
			{"0", "2000-01-02", "2000-01-01", "22008", "stride must be greater than zero"},
			{"-1 second", "2000-01-02", "2000-01-01", "22008", "stride must be greater than zero"},
			{"1 day -24 hours", "2000-01-02", "2000-01-01", "22008", "stride must be greater than zero"},
			{"1 mon", "2000-01-02", "2000-01-01", "0A000", "timestamps cannot be binned into intervals containing months or years"},
			{"1 mon -30 days", "2000-01-02", "2000-01-01", "0A000", "timestamps cannot be binned into intervals containing months or years"},
			{"1 year", "2000-01-02", "2000-01-01", "0A000", "timestamps cannot be binned into intervals containing months or years"},
			{"-1 mon -1 day", "2000-01-02", "2000-01-01", "0A000", "timestamps cannot be binned into intervals containing months or years"},
			{"infinity", "2000-01-02", "2000-01-01", "22008", "timestamps cannot be binned into infinite intervals"},
			{"-infinity", "2000-01-02", "2000-01-01", "22008", "timestamps cannot be binned into infinite intervals"},
			{"1 day", "2000-01-02", "infinity", "22008", "origin out of range"},
			{"1 day", "2000-01-02", "-infinity", "22008", "origin out of range"},
			{"0", "2000-01-02", "infinity", "22008", "origin out of range"},
			{"1 mon", "2000-01-02", "-infinity", "22008", "origin out of range"},
			{"infinity", "2000-01-02", "infinity", "22008", "origin out of range"},
			{"15 minutes", "294276-12-30", "4000-12-20 BC", "22008", "interval out of range"},
			{"200000000 days", "2024-02-01", "2024-01-01", "22008", "interval out of range"},
			{"365000 days", "4400-01-01 BC", "4000-01-01 BC", "22008", "timestamp out of range"},
			// Individual extrema are finite, not reserved infinity tuples.
			{"2147483647 months", "2000-01-02", "2000-01-01", "0A000", "timestamps cannot be binned into intervals containing months or years"},
			{"2147483647 days", "2000-01-02", "2000-01-01", "22008", "interval out of range"},
			{"106751991 days 24 hours", "2000-01-02", "2000-01-01", "22008", "interval out of range"},
			{"106751992 days -24 hours", "2000-01-02", "2000-01-01", "22008", "interval out of range"},
		} {
			t.Run(typ+"/"+tc.stride+"/"+tc.origin+"/"+tc.source, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				datetimeExtractFailure(t, p, b, c, "SELECT "+datetimeBinCall(tc.stride, typ, tc.source, tc.origin), d, "execute")
				datetimeExtractFailure(t, p, b, c, "SELECT date_bin($1::interval,$2::"+typ+",$3::"+typ+")", d, "execute", tc.stride, tc.source, tc.origin)
			})
		}
		for _, source := range []string{"infinity", "-infinity"} {
			for _, stride := range []string{"0", "-1 day", "1 mon", "infinity", "-infinity"} {
				for _, origin := range []string{"2000-01-01", "infinity", "-infinity"} {
					datetimeTruncValue(t, p, b, c, datetimeBinCall(stride, typ, source, origin)+"::text", "TEXT", source)
				}
			}
		}
	}
	missing := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct{ q, signature, position string }{
		{"SELECT date_bin('1 day',TIMESTAMP '2001-01-01',1)", "date_bin(unknown, timestamp without time zone, integer)", "8"},
		{"SELECT date_bin('1 day',TIMESTAMPTZ '2001-01-01+00',1)", "date_bin(unknown, timestamp with time zone, integer)", "8"},
		{"SELECT date_bin('1 day',TIMESTAMP '2001-01-01','x'::text)", "date_bin(unknown, timestamp without time zone, text)", "8"},
		{"SELECT date_bin('1 day',TIMESTAMP '2001-01-01',TIME '12:00')", "date_bin(unknown, timestamp without time zone, time without time zone)", "8"},
		{"SELECT date_bin('1 day'::text,TIMESTAMP '2001-01-01',TIMESTAMP '2000-01-01')", "date_bin(text, timestamp without time zone, timestamp without time zone)", "8"},
		{"SELECT date_bin()", "date_bin()", "8"},
		{"SELECT date_bin('1 day')", "date_bin(unknown)", "8"},
		{"SELECT date_bin('1 day',TIMESTAMP '2001-01-01')", "date_bin(unknown, timestamp without time zone)", "8"},
		{"SELECT date_bin('1 day',TIMESTAMP '2001-01-01',TIMESTAMP '2000-01-01',1)", "date_bin(unknown, timestamp without time zone, timestamp without time zone, integer)", "8"},
		{"/*é*/ SELECT date_bin('1 day',TIMESTAMP '2001-01-01',1)", "date_bin(unknown, timestamp without time zone, integer)", "14"},
		{"SELECT pg_catalog.date_bin('1 day',TIMESTAMP '2001-01-01',1)", "pg_catalog.date_bin(unknown, timestamp without time zone, integer)", "8"},
		{"SELECT public.date_bin('1 day',TIMESTAMP '2001-01-01',TIMESTAMP '2000-01-01')", "public.date_bin(unknown, timestamp without time zone, timestamp without time zone)", "8"},
		{`SELECT "Date_Bin"('1 day',TIMESTAMP '2001-01-01',TIMESTAMP '2000-01-01')`, "Date_Bin(unknown, timestamp without time zone, timestamp without time zone)", "8"},
		{"SELECT date_bin(stride=>'1 day',source=>TIMESTAMP '2001-01-01',origin=>TIMESTAMP '2000-01-01')", "date_bin(stride => unknown, source => timestamp without time zone, origin => timestamp without time zone)", "8"},
		{"SELECT date_bin(VARIADIC '{1 day}'::interval[])", "date_bin(interval[])", "8"},
	} {
		d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
		d.Hint, d.Position = missing, tc.position
		datetimeExtractFailure(t, p, b, c, tc.q, d, "prepare")
	}
	// Literal conversion must fail in analysis even beside a strict NULL.
	prefix := "SELECT date_bin(NULL::interval,TIMESTAMP "
	d := fixture.ControlsReferenceDiagnostic("22007", `invalid input syntax for type timestamp: "not-a-timestamp"`)
	d.Position = fixture.Position(len([]rune(prefix)) + 1)
	datetimeExtractFailure(t, p, b, c, prefix+"'not-a-timestamp',TIMESTAMP '2000-01-01')", d, "prepare")
	q := "SELECT date_bin($1,$1,TIMESTAMP '2000-01-01')"
	d = fixture.ControlsReferenceDiagnostic("42P08", "inconsistent types deduced for parameter $1")
	d.Detail, d.Position = "interval versus timestamp without time zone", "20"
	datetimeExtractFailure(t, p, b, c, q, d, "prepare", nil)
	// Qualification remains authoritative with pg_catalog last in the path.
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path TO public,pg_catalog", 0))
	datetimeTruncValue(t, p, b, c, "pg_catalog.date_bin('1 day',TIMESTAMP '2000-01-02',TIMESTAMP '2000-01-01')", "TIMESTAMP", "2000-01-02T00:00:00Z")
}

func TestReferenceDatetimeBinDiagnostics(t *testing.T) {
	datetimeBinScenario(t, datetimeBinDiagnostics)
}
