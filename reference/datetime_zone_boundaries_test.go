package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
	"time"
)

// Independently fixed release expectations, confirmed by Main's pinned oracle
// report tests-2.md/artifact19. C int32 narrowing and the INT_MIN formatting
// anomaly are SQL results, not grounds for importing literal/SET offset limits.
func TestReferenceDatetimeZoneLargeIntervalOffsets(t *testing.T) {
	datetimeZoneScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ seconds, stamp, instant, clock string }{
			{"2147483647", "1935-12-14 08:45:53+00", "2072-01-19 15:14:07", "15:14:07+596523:14:07"},
			{"2147483648", "2072-01-19 15:14:08+00", "2072-01-19 15:14:08", "08:45:52+4294370773:4294967282:4294967288"},
			{"-2147483648", "2072-01-19 15:14:08+00", "2072-01-19 15:14:08", "08:45:52+4294370773:4294967282:4294967288"},
			{"-2147483649", "1935-12-14 08:45:53+00", "2072-01-19 15:14:07", "15:14:07+596523:14:07"},
			{"4294967296", "2004-01-01 12:00:00+00", "2004-01-01 12:00:00", "12:00:00+00"},
		} {
			t.Run(tc.seconds, func(t *testing.T) {
				for _, v := range []struct{ source, typ, want, layout string }{
					{"TIMESTAMP '2004-01-01 12:00'", "TIMESTAMPTZ", tc.stamp, "2006-01-02 15:04:05Z07"},
					{"TIMESTAMPTZ '2004-01-01 12:00+00'", "TIMESTAMP", tc.instant, "2006-01-02 15:04:05"},
				} {
					raw, err := time.Parse(v.layout, v.want)
					if err != nil {
						t.Fatal(err)
					}
					expr := "timezone(INTERVAL '" + tc.seconds + " seconds'," + v.source + ")"
					s := referenceSelect("SELECT "+expr+" AS raw,("+expr+")::TEXT AS rendered FROM tz_witness WHERE id=1", []string{"raw", "rendered"}, []string{v.typ, "TEXT"}, [][]any{{raw.UTC(), v.want}})
					datetimeArithmeticBoth(t, p, b, c, s)
					datetimeZoneText(t, p, b, c, v.source+" AT TIME ZONE INTERVAL '"+tc.seconds+" seconds'", v.want)
				}
				// lib/pq cannot decode huge raw TIMETZ offsets. The full server
				// text is asserted instead; ordinary codec failures are not SQL errors.
				datetimeZoneText(t, p, b, c, "timezone(INTERVAL '"+tc.seconds+" seconds',TIMETZ '12:00+00')", tc.clock)
				datetimeZoneText(t, p, b, c, "TIMETZ '12:00+00' AT TIME ZONE INTERVAL '"+tc.seconds+" seconds'", tc.clock)
			})
		}
		for _, zone := range []string{"MSK", "Europe/Moscow"} {
			datetimeZoneText(t, p, b, c, "date_add(TIMESTAMPTZ '2010-03-27 09:00+00',INTERVAL '1 day','"+zone+"')", "2010-03-28 08:00:00+00")
		}
	})
}

func TestReferenceDatetimeZoneSignatureCatalogAndResultTypmods(t *testing.T) {
	datetimeZoneScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		if b.pg {
			// Oracle identity witness only: this part does not expand native pg_proc.
			s := referenceSelect("SELECT oid::TEXT,proname,proargtypes::TEXT,prorettype::TEXT,proisstrict,provolatile::TEXT FROM pg_catalog.pg_proc WHERE oid IN (2069,2070,1159,1026,2037,2038,6335,6334,6336,6221,6222,6223,6273) ORDER BY oid", []string{"oid", "proname", "proargtypes", "prorettype", "proisstrict", "provolatile"}, []string{"TEXT", "NAME", "TEXT", "TEXT", "BOOL", "TEXT"}, [][]any{
				{"1026", "timezone", "1186 1184", "1114", true, "i"},
				{"1159", "timezone", "25 1184", "1114", true, "i"},
				{"2037", "timezone", "25 1266", "1266", true, "s"},
				{"2038", "timezone", "1186 1266", "1266", true, "i"},
				{"2069", "timezone", "25 1114", "1184", true, "i"},
				{"2070", "timezone", "1186 1114", "1184", true, "i"},
				{"6221", "date_add", "1184 1186", "1184", true, "s"},
				{"6222", "date_add", "1184 1186 25", "1184", true, "i"},
				{"6223", "date_subtract", "1184 1186", "1184", true, "s"},
				{"6273", "date_subtract", "1184 1186 25", "1184", true, "i"},
				{"6334", "timezone", "1184", "1114", true, "s"},
				{"6335", "timezone", "1114", "1184", true, "s"},
				{"6336", "timezone", "1266", "1266", true, "s"},
			})
			datetimeArithmeticBoth(t, p, b, c, s)
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE VIEW dz_result_precision AS SELECT timezone('UTC',TIMESTAMP(3) '2004-01-01 00:00:00.1235') AS z,timezone('UTC',TIMESTAMPTZ(3) '2004-01-01 00:00:00.1235+00') AS s,timezone(INTERVAL '0',TIMETZ(3) '12:00:00.1235+00') AS q,date_add(TIMESTAMPTZ(3) '2004-01-01 00:00:00.1235+00',INTERVAL '0','UTC') AS a FROM tz_witness", 0))
		// PostgreSQL catalog typmods are supplementary oracle research, not a
		// request to expand native pg_attribute. Native public columns retain
		// their independently declared temporal type/metadata contracts.
		if b.pg {
			datetimeArithmeticBoth(t, p, b, c, referenceSelect("SELECT attname,atttypid::TEXT,atttypmod FROM pg_catalog.pg_attribute WHERE attrelid='dz_result_precision'::REGCLASS AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid", "atttypmod"}, []string{"NAME", "TEXT", "INT4"}, [][]any{{"z", "1184", int64(-1)}, {"s", "1114", int64(-1)}, {"q", "1266", int64(-1)}, {"a", "1184", int64(-1)}}))
		} else {
			values := referenceSelect("SELECT z,s,q,a FROM dz_result_precision", []string{"z", "s", "q", "a"}, []string{"TIMESTAMPTZ", "TIMESTAMP", "TIMETZ", "TIMESTAMPTZ"}, [][]any{{time.Date(2004, 1, 1, 0, 0, 0, 124000000, time.UTC), time.Date(2004, 1, 1, 0, 0, 0, 124000000, time.UTC), "12:00:00.124+00", time.Date(2004, 1, 1, 0, 0, 0, 124000000, time.UTC)}})
			values.checkMetadata = func(t *testing.T, rows *sql.Rows) {
				columns, err := rows.ColumnTypes()
				if err != nil || len(columns) != 4 {
					t.Fatal(columns, err)
				}
				for i, want := range []string{"TIMESTAMPTZ", "TIMESTAMP", "TIMETZ", "TIMESTAMPTZ"} {
					if columns[i].DatabaseTypeName() != want {
						t.Fatal(columns[i], want)
					}
					scan := reflect.TypeFor[time.Time]()
					if i == 2 {
						scan = reflect.TypeFor[string]()
					}
					if columns[i].ScanType() != scan {
						t.Fatal(columns[i].ScanType(), scan)
					}
					precision, scale, decimal := columns[i].DecimalSize()
					if precision != 0 || scale != 0 || decimal {
						t.Fatal(precision, scale, decimal)
					}
				}
			}
			datetimeArithmeticBoth(t, p, b, c, values)
		}
		datetimeArithmeticBoth(t, p, b, c, referenceSelect("SELECT z::TEXT AS z,s::TEXT AS s,q::TEXT AS q,a::TEXT AS a FROM dz_result_precision", []string{"z", "s", "q", "a"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"2004-01-01 00:00:00.124+00", "2004-01-01 00:00:00.124", "12:00:00.124+00", "2004-01-01 00:00:00.124+00"}}))
	})
}

func TestReferenceDatetimeZoneTypedIntervalOperatorResult(t *testing.T) {
	datetimeZoneScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, expression := range []string{
			"TIMESTAMP '2026-10-08 00:00:00' AT TIME ZONE INTERVAL '02:00' HOUR TO MINUTE",
			"timezone(INTERVAL '02:00' HOUR TO MINUTE,TIMESTAMP '2026-10-08 00:00:00')",
		} {
			datetimeZoneText(t, p, b, c, expression, "2026-10-07 22:00:00+00")
		}
	})
}
