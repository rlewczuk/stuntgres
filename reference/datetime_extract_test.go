package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

// These explicit constants describe civil 2000-02-29 and a 17:12:28.123456
// clock, not results computed by either SQL backend. Each absent entry is an
// independently specified recognized-but-unsupported field.
func TestReferenceDatetimeExtractApplicability(t *testing.T) {
	calendar := map[string]string{"century": "20", "day": "29", "decade": "200", "dow": "2", "doy": "60", "isodow": "2", "isoyear": "2000", "julian": "2451604", "millennium": "2", "month": "2", "quarter": "1", "week": "9", "year": "2000"}
	clock := map[string]string{"hour": "17", "minute": "12", "second": "28.123456", "milliseconds": "28123.456", "microseconds": "28123456"}
	for _, source := range []struct {
		name, typ, diagnostic, literal string
		values                         map[string]string
	}{
		{"date", "date", "date", "2000-02-29", calendar},
		{"time", "time", "time without time zone", "17:12:28.123456", clock},
		{"timetz", "timetz", "time with time zone", "17:12:28.123456+05:45", clock},
		{"timestamp", "timestamp", "timestamp without time zone", "2000-02-29 17:12:28.123456", calendar},
		{"timestamptz", "timestamptz", "timestamp with time zone", "2000-02-29 17:12:28.123456+00", calendar},
		{"interval", "interval", "interval", "2 years 13 months 13 days 80 minutes 28.123456 seconds", map[string]string{"century": "0", "day": "13", "decade": "0", "epoch": "98392828.123456", "hour": "1", "microseconds": "28123456", "millennium": "0", "milliseconds": "28123.456", "minute": "20", "month": "1", "quarter": "1", "second": "28.123456", "week": "1", "year": "3"}},
	} {
		t.Run(source.name, func(t *testing.T) {
			datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				values := make(map[string]string)
				for k, v := range source.values {
					values[k] = v
				}
				if source.name == "timestamp" || source.name == "timestamptz" {
					for k, v := range clock {
						values[k] = v
					}
					values["epoch"], values["julian"] = "951844348.123456", "2451604.71699216962962962963"
				}
				switch source.name {
				case "date":
					values["epoch"] = "951782400"
				case "time":
					values["epoch"] = "61948.123456"
				case "timetz":
					values["epoch"] = "41248.123456"
				}
				if source.name == "timetz" {
					values["timezone"], values["timezone_hour"], values["timezone_minute"] = "20700", "5", "45"
				}
				if source.name == "timestamptz" {
					values["timezone"], values["timezone_hour"], values["timezone_minute"] = "0", "0", "0"
				}
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE dx_input(v "+source.typ+")", 0))
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO dx_input VALUES($1::"+source.typ+")", 1, source.literal))
				for _, api := range []string{"extract", "date_part"} {
					for _, field := range datetimeExtractFields {
						t.Run(api+"/"+field, func(t *testing.T) {
							want, supported := values[field]
							diagnostic := source.diagnostic
							if api == "date_part" && source.name == "date" {
								diagnostic = "timestamp without time zone"
								if _, ok := clock[field]; ok {
									want, supported = "0", true
								}
							}
							for _, route := range []string{"literal", "column", "parameter"} {
								t.Run(route, func(t *testing.T) {
									expr, suffix := source.typ+" '"+source.literal+"'", ""
									var args []any
									if route == "column" {
										expr, suffix = "v", " FROM dx_input"
									}
									if route == "parameter" {
										expr, args = "$1::"+source.typ, []any{source.literal}
									}
									q := "SELECT " + datetimeExtractSQL(api, field, expr) + suffix
									if !supported {
										d := datetimeExtractFieldDiagnostic("0A000", field, diagnostic)
										if source.name == "date" && api == "date_part" && route != "column" {
											d = datetimeExtractDateWrapperDiagnostic(d)
										}
										datetimeExtractFailure(t, p, b, c, q, d, "execute", args...)
										return
									}
									typ, cell := "NUMERIC", any(want)
									if api == "date_part" {
										typ, cell = "FLOAT8", datetimeExtractFloat(t, want)
									}
									datetimeExtractBoth(t, p, b, c, referenceSelect(q, []string{api}, []string{typ}, [][]any{{cell}}, args...))
								})
							}
						})
					}
				}
			})
		})
	}
}

func TestReferenceDatetimeExtractCalendar(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			field, source, numeric string
			floating               float64
		}{
			{"doy", "DATE '2000-02-29'", "60", 60}, {"doy", "DATE '2001-03-01'", "60", 60},
			{"dow", "DATE '2001-02-18'", "0", 0}, {"isodow", "DATE '2001-02-18'", "7", 7},
			{"week", "DATE '2005-01-01'", "53", 53}, {"isoyear", "DATE '2005-01-01'", "2004", 2004},
			{"week", "DATE '2006-01-01'", "52", 52}, {"isoyear", "DATE '2006-01-01'", "2005", 2005},
			{"week", "DATE '2006-01-02'", "1", 1}, {"isoyear", "DATE '2006-01-02'", "2006", 2006},
			{"week", "DATE '2012-12-31'", "1", 1}, {"isoyear", "DATE '2012-12-31'", "2013", 2013},
			{"century", "DATE '2000-01-01'", "20", 20}, {"century", "DATE '2001-01-01'", "21", 21},
			{"millennium", "DATE '2000-01-01'", "2", 2}, {"millennium", "DATE '2001-01-01'", "3", 3},
			{"year", "DATE '0001-01-01 BC'", "-1", -1}, {"year", "DATE '0001-01-01 AD'", "1", 1},
			{"century", "DATE '0001-01-01 BC'", "-1", -1}, {"millennium", "DATE '0001-01-01 BC'", "-1", -1},
			{"decade", "DATE '0001-01-01 BC'", "0", 0}, {"decade", "DATE '0002-01-01 BC'", "-1", -1},
			{"day", "TIMESTAMP '1999-12-31 23:59:59.999999'", "31", 31},
		} {
			t.Run(fmt.Sprintf("%s/%s", tc.field, tc.source), func(t *testing.T) { datetimeExtractPair(t, p, b, c, tc.field, tc.source, tc.numeric, tc.floating) })
		}
		q := "SELECT EXTRACT(year FROM DATE '400000-01-01')"
		datetimeExtractBoth(t, p, b, c, referenceSelect(q, []string{"extract"}, []string{"NUMERIC"}, [][]any{{"400000"}}))
	})
}
