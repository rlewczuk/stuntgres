package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDatetimeExtractFeatureSmoke(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE dx_smoke(d date,t time,z timetz,s timestamp,a timestamptz,i interval)", 0))
		run(referenceExec("INSERT INTO dx_smoke VALUES(DATE '2000-02-29',TIME '17:12:28.123456',TIMETZ '17:12:28.123456+05:45',TIMESTAMP '2005-01-01 17:12:28.123456',TIMESTAMPTZ '2020-01-01 00:00+00',INTERVAL '13 days 24 hours')", 1))
		q := "SELECT EXTRACT(doy FROM d) AS nd,date_part('doy',d) AS fd,EXTRACT(second FROM t) AS nt,date_part('second',t) AS ft,EXTRACT(timezone FROM z) AS nz,date_part('timezone',z) AS fz,EXTRACT(week FROM s) AS ns,date_part('isoyear',s) AS fs,EXTRACT(epoch FROM a) AS na,date_part('epoch',a) AS fa,EXTRACT(hour FROM i) AS ni,date_part('week',i) AS fi FROM dx_smoke"
		datetimeExtractBoth(t, p, b, c, referenceSelect(q, []string{"nd", "fd", "nt", "ft", "nz", "fz", "ns", "fs", "na", "fa", "ni", "fi"}, []string{"NUMERIC", "FLOAT8", "NUMERIC", "FLOAT8", "NUMERIC", "FLOAT8", "NUMERIC", "FLOAT8", "NUMERIC", "FLOAT8", "NUMERIC", "FLOAT8"}, [][]any{{"60", float64(60), "28.123456", float64(28.123456), "20700", float64(20700), "53", float64(2004), "1577836800.000000", float64(1577836800), "24", float64(1)}}))
		for _, tc := range []struct {
			field, source, n string
			f                float64
		}{
			{"dow", "DATE '2001-02-18'", "0", 0}, {"isodow", "DATE '2001-02-18'", "7", 7},
			{"milliseconds", "TIME '17:12:28.123456'", "28123.456", 28123.456},
			{"microseconds", "TIME '17:12:28.123456'", "28123456", 28123456},
			{"hour", "TIME '24:00'", "24", 24}, {"day", "INTERVAL '13 days 24 hours'", "13", 13},
			{"epoch", "INTERVAL '1 year'", "31557600.000000", 31557600},
			{"century", "DATE '0001-01-01 BC'", "-1", -1},
			{"millennium", "DATE '0001-01-01 BC'", "-1", -1},
			{"decade", "DATE '0002-01-01 BC'", "-1", -1},
		} {
			datetimeExtractPair(t, p, b, c, tc.field, tc.source, tc.n, tc.f)
		}
		datetimeExtractFailure(t, p, b, c, "SELECT EXTRACT(hour FROM DATE '2000-01-01')", datetimeExtractFieldDiagnostic("0A000", "hour", "date"), "execute")
		datetimeExtractBoth(t, p, b, c, referenceSelect("SELECT date_part('hour',DATE '2000-01-01')", []string{"date_part"}, []string{"FLOAT8"}, [][]any{{float64(0)}}))
		datetimeExtractBoth(t, p, b, c, referenceSelect("SELECT EXTRACT(year FROM TIMESTAMP 'infinity'),date_part('second',TIMESTAMP 'infinity')", []string{"extract", "date_part"}, []string{"NUMERIC", "FLOAT8"}, [][]any{{"Infinity", nil}}))
		datetimeExtractBoth(t, p, b, c, referenceSelect("SELECT EXTRACT(bogus FROM NULL::timestamp),date_part('bogus',NULL::timestamp)", []string{"extract", "date_part"}, []string{"NUMERIC", "FLOAT8"}, [][]any{{nil, nil}}))
		datetimeExtractFailure(t, p, b, c, "SELECT date_part('year',DATE '400000-01-01')", datetimeExtractDateWrapperDiagnostic(controlsReferenceDiagnostic("22008", "date out of range for timestamp")), "execute")
		datetimeExtractHeld(t, p, b, c)
		datetimeExtractZones(t, p, b, c)
		datetimeExtractAtomicity(t, p, b, c)
		// Dump/replay is definition-only here; selected source/atomicity witnesses
		// above are already asserted and need not pollute the portable dump.
		run(referenceExec("DROP TABLE dx_smoke", 0))
		run(referenceExec("DROP TABLE dx_atomic", 0))
		run(referenceExec("SET TIME ZONE 'UTC'", 0))
		datetimeExtractStored(t, p, b, c)
	})
}
