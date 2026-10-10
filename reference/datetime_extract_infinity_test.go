package reference_test

import (
	"database/sql"
	"math"
	"strings"
	"testing"
)

func TestReferenceDatetimeExtractInfinity(t *testing.T) {
	for _, typ := range []string{"date", "timestamp", "timestamptz", "interval"} {
		for _, sign := range []string{"infinity", "-infinity"} {
			t.Run(typ+"/"+sign, func(t *testing.T) {
				datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
					for _, api := range []string{"extract", "date_part"} {
						for _, field := range datetimeExtractFields {
							t.Run(api+"/"+field, func(t *testing.T) {
								monotone := " epoch julian year isoyear decade century millennium "
								nulls := " day month quarter week dow isodow doy hour microseconds milliseconds minute second timezone timezone_hour timezone_minute "
								diagnostic := "timestamp without time zone"
								if typ == "timestamptz" {
									diagnostic = "timestamp with time zone"
								}
								if typ == "date" && api == "extract" {
									diagnostic, nulls = "date", " day month quarter week dow isodow doy "
								}
								if typ == "interval" {
									diagnostic, monotone, nulls = "interval", " epoch hour day year decade century millennium ", " microseconds milliseconds second minute week month quarter "
								}
								q := "SELECT " + datetimeExtractSQL(api, field, typ+" '"+sign+"'")
								var cell any
								typeName := "NUMERIC"
								if api == "date_part" {
									typeName = "FLOAT8"
								}
								if strings.Contains(monotone, " "+field+" ") {
									cell = "Infinity"
									if sign == "-infinity" {
										cell = "-Infinity"
									}
									if api == "date_part" {
										cell = math.Inf(1)
										if sign == "-infinity" {
											cell = math.Inf(-1)
										}
									}
								} else if !strings.Contains(nulls, " "+field+" ") {
									datetimeExtractFailure(t, p, b, c, q, datetimeExtractFieldDiagnostic("0A000", field, diagnostic), "execute")
									return
								}
								datetimeExtractBoth(t, p, b, c, referenceSelect(q, []string{api}, []string{typeName}, [][]any{{cell}}))
							})
						}
					}
				})
			})
		}
	}
}

func TestReferenceDatetimeExtractAliasesAndDateWrapper(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			field, source, numeric string
			floating               float64
		}{
			{"m", "TIMESTAMP '2000-02-29 17:12:28.123456'", "12", 12},
			{"mm", "TIMESTAMP '2000-02-29 17:12:28.123456'", "12", 12},
			{"mon", "TIMESTAMP '2000-02-29 17:12:28.123456'", "2", 2},
			{"MiNuTeS", "TIME '17:12:28.123456'", "12", 12},
			{"microseconds_extra", "TIME '17:12:28.123456'", "28123456", 28123456},
			{"timezone_hour_extra", "TIMETZ '00:00-05:30'", "-5", -5},
		} {
			t.Run(tc.field, func(t *testing.T) { datetimeExtractPair(t, p, b, c, tc.field, tc.source, tc.numeric, tc.floating) })
		}
		for _, q := range []string{"SELECT EXTRACT(YEAR FROM DATE '2000-02-29')", "SELECT EXTRACT(\"year\" FROM DATE '2000-02-29')", "SELECT EXTRACT('year' FROM DATE '2000-02-29')", "SELECT EXTRACT(year FROM (DATE '2000-02-28'+1))", "SELECT pg_catalog.extract('year',DATE '2000-02-29')"} {
			t.Run(q, func(t *testing.T) {
				datetimeExtractBoth(t, p, b, c, referenceSelect(q, []string{"extract"}, []string{"NUMERIC"}, [][]any{{"2000"}}))
			})
		}
		datetimeExtractBoth(t, p, b, c, referenceSelect("SELECT pg_catalog.date_part('hour',DATE '2000-01-01')", []string{"date_part"}, []string{"FLOAT8"}, [][]any{{float64(0)}}))
		datetimeExtractFailure(t, p, b, c, "SELECT EXTRACT(hour FROM DATE '2000-01-01')", datetimeExtractFieldDiagnostic("0A000", "hour", "date"), "execute")
		datetimeExtractFailure(t, p, b, c, "SELECT date_part('year',DATE '400000-01-01')", datetimeExtractDateWrapperDiagnostic(controlsReferenceDiagnostic("22008", "date out of range for timestamp")), "execute")
		for _, field := range []string{"year_extra", " year", "year ", "BOGUS", "ÉYEAR", strings.Repeat("X", 64), strings.Repeat("X", 62) + "éZ"} {
			normalized := field
			if field == "BOGUS" {
				normalized = "bogus"
			}
			if field == "ÉYEAR" {
				normalized = "Éyear"
			}
			if strings.HasPrefix(field, "X") {
				normalized = strings.Repeat("x", 63)
				if strings.Contains(field, "é") {
					normalized = strings.Repeat("x", 62)
				}
			}
			for _, api := range []string{"extract", "date_part"} {
				q := "SELECT " + datetimeExtractSQL(api, field, "TIMESTAMP '2000-01-01'")
				t.Run(api+"/"+field, func(t *testing.T) {
					datetimeExtractFailure(t, p, b, c, q, datetimeExtractFieldDiagnostic("22023", normalized, "timestamp without time zone"), "execute")
				})
			}
		}
		for _, field := range []string{"timezone", "bogus"} {
			code := "0A000"
			if field == "bogus" {
				code = "22023"
			}
			datetimeExtractFailure(t, p, b, c, "SELECT date_part('"+field+"',DATE '2000-01-01')", datetimeExtractDateWrapperDiagnostic(datetimeExtractFieldDiagnostic(code, field, "timestamp without time zone")), "execute")
		}
	})
}
