package reference_test

import (
	"testing"
	"time"
)

// REL_18_0 system_functions.sql defines the DATE date_part wrapper, temporal
// commutators, and interval OVERLAPS overloads in SQL. These witnesses protect
// real body calls/operators rather than internal C implementation helpers.
func TestReferenceObjectGrantRoutineBodyTemporal(t *testing.T) {
	jan1 := time.Date(2001, time.January, 1, 12, 0, 0, 0, time.UTC)
	jan2 := time.Date(2001, time.January, 2, 12, 0, 0, 0, time.UTC)
	for _, tc := range []objectGrantBodyCase{
		{
			name: "date_part_date", columns: "d DATE,ts TIMESTAMP", values: "DATE '2001-01-01',TIMESTAMP '2001-01-01 12:00'",
			expression: "date_part('year',d)", nullExpression: "date_part('year',NULL::DATE)", deadExpression: "CASE WHEN FALSE THEN date_part('year',d) ELSE 2001::FLOAT8 END",
			resultType: "FLOAT8", value: float64(2001), target: "pg_catalog.date_part(text,timestamp without time zone)", deniedName: "date_part",
			primitive: "date_part('hour',ts)", primitiveType: "FLOAT8", primitiveValue: float64(12),
			independent: "extract(year FROM d)", independentType: "NUMERIC", independentValue: "2001",
		},
		{
			name: "time_date_commutator", columns: "tm TIME,d DATE", values: "TIME '12:00',DATE '2001-01-01'",
			expression: "tm + d", nullExpression: "NULL::TIME + DATE '2001-01-01'", deadExpression: "CASE WHEN FALSE THEN tm + d ELSE TIMESTAMP '2001-01-01 12:00' END",
			resultType: "TIMESTAMP", value: jan1, target: "pg_catalog.datetime_pl(date,time without time zone)", deniedName: "datetime_pl",
			primitive: "d + tm", primitiveType: "TIMESTAMP", primitiveValue: jan1,
			independent: "d + 1 = DATE '2001-01-02'", independentType: "BOOL", independentValue: true,
		},
		{
			name: "interval_timestamp_commutator", columns: "iv INTERVAL,ts TIMESTAMP", values: "INTERVAL '1 day',TIMESTAMP '2001-01-01 12:00'",
			expression: "iv + ts", nullExpression: "NULL::INTERVAL + TIMESTAMP '2001-01-01 12:00'", deadExpression: "CASE WHEN FALSE THEN iv + ts ELSE TIMESTAMP '2001-01-02 12:00' END",
			resultType: "TIMESTAMP", value: jan2, target: "pg_catalog.timestamp_pl_interval(timestamp without time zone,interval)", deniedName: "timestamp_pl_interval",
			primitive: "ts + iv", primitiveType: "TIMESTAMP", primitiveValue: jan2,
			independent: "date_part('hour',ts)", independentType: "FLOAT8", independentValue: float64(12),
		},
		{
			name: "overlaps_interval_arithmetic", columns: "a TIMESTAMP,iv INTERVAL,b TIMESTAMP,c TIMESTAMP", values: "TIMESTAMP '2001-01-01',INTERVAL '2 days',TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-04'",
			expression: "(a,iv) OVERLAPS (b,c)", deadExpression: "CASE WHEN FALSE THEN (a,iv) OVERLAPS (b,c) ELSE TRUE END",
			resultType: "BOOL", value: true, target: "pg_catalog.timestamp_pl_interval(timestamp without time zone,interval)", deniedName: "timestamp_pl_interval",
			primitive: "a + iv", primitiveType: "TIMESTAMP", primitiveValue: time.Date(2001, time.January, 3, 0, 0, 0, 0, time.UTC),
			independent: "(a,b) OVERLAPS (b,c)", independentType: "BOOL", independentValue: false,
		},
		{
			name: "overlaps_endpoint_body", columns: "a TIMESTAMP,iv INTERVAL,b TIMESTAMP,c TIMESTAMP", values: "TIMESTAMP '2001-01-01',INTERVAL '2 days',TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-04'",
			expression: "(a,iv) OVERLAPS (b,c)", deadExpression: "CASE WHEN FALSE THEN (a,iv) OVERLAPS (b,c) ELSE TRUE END",
			resultType: "BOOL", value: true, target: "pg_catalog.overlaps(timestamp without time zone,timestamp without time zone,timestamp without time zone,timestamp without time zone)", deniedName: "overlaps",
			primitive: "(a,c) OVERLAPS (b,c)", primitiveType: "BOOL", primitiveValue: true,
			independent: "a + iv", independentType: "TIMESTAMP", independentValue: time.Date(2001, time.January, 3, 0, 0, 0, 0, time.UTC),
		},
	} {
		t.Run(tc.name, func(t *testing.T) { objectGrantBodyConsumer(t, tc) })
	}
}
