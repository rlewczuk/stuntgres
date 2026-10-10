package reference_test

import (
	"database/sql"
	"testing"
)

// Root's pinned PostgreSQL18.0 observations (tests-1) confirm the independently
// calculated rational/component expectations, including release-specific edges:
// NUMERIC timestamp epoch's overflow fallback rounds the largest timestamp;
// negative int32-min months quarter negation wraps before remainder calculation.
func TestReferenceDatetimeExtractExtremePrecision(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			field, source, numeric string
			floating               float64
		}{
			{"julian", "TIMESTAMP '2006-01-01'", "2453737.0000000000000000000000000000", 2453737},
			{"julian", "TIMESTAMP '2006-01-01 00:00:00.000001'", "2453737.0000000000115740740740740741", 2453737},
			{"julian", "TIMESTAMP '0001-01-01 00:00:00.000001 BC'", "1721060.0000000000115740740740740741", 1721060},
			{"julian", "TIMESTAMP '294276-12-31 23:59:59.999999'", "109203527.99999999998842592593", 109203528},
			{"epoch", "TIMESTAMP '294276-12-31 23:59:59.999999'", "9224318016000.000000", 9224318016000},
			{"epoch", "TIMESTAMP '4713-01-01 00:00:00.000001 BC'", "-210863519999.999999", -210863520000},
			{"year", "INTERVAL '-2147483648 months'", "-178956970", -178956970},
			{"month", "INTERVAL '-2147483648 months'", "-8", -8},
			{"quarter", "INTERVAL '-2147483648 months'", "1", 1},
			{"epoch", "INTERVAL '-2147483648 months'", "-5647452497208000.000000", -5647452497208000},
			{"year", "INTERVAL '2147483647 months'", "178956970", 178956970},
			{"month", "INTERVAL '2147483647 months'", "7", 7},
			{"epoch", "INTERVAL '2147483647 months'", "5647452494616000.000000", 5647452494616000},
			{"day", "INTERVAL '-2147483648 days'", "-2147483648", -2147483648},
			{"epoch", "INTERVAL '-2147483648 days'", "-185542587187200.000000", -185542587187200},
			{"day", "INTERVAL '2147483647 days'", "2147483647", 2147483647},
			{"epoch", "INTERVAL '2147483647 days'", "185542587100800.000000", 185542587100800},
			{"hour", "INTERVAL '2562047788 hours 54.775807 seconds'", "2562047788", 2562047788},
			{"second", "INTERVAL '2562047788 hours 54.775807 seconds'", "54.775807", 54.775807},
			{"epoch", "INTERVAL '2562047788 hours 54.775807 seconds'", "9223372036854.775807", 9223372036854.775},
			{"hour", "INTERVAL '-2562047788 hours -54.775808 seconds'", "-2562047788", -2562047788},
			{"epoch", "INTERVAL '-2562047788 hours -54.775808 seconds'", "-9223372036854.775808", -9223372036854.775},
			{"epoch", "INTERVAL '2147483647 months -2147483648 days -2562047788 hours -54.775807 seconds'", "5452686535391945.224193", 5452686535391945},
		} {
			t.Run(tc.field+"/"+tc.source, func(t *testing.T) { datetimeExtractPair(t, p, b, c, tc.field, tc.source, tc.numeric, tc.floating) })
		}
	})
}
