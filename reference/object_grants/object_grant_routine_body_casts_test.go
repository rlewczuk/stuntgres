package reference_test

import "testing"

// These are real method-f casts in REL_18_0 pg_cast.dat, not CoerceViaIO
// implementation helpers. Their pg_proc owners remain meaningful even where
// standalone function-call syntax is not an existing native provider.
func TestReferenceObjectGrantRoutineBodyCastDate(t *testing.T) {
	for _, tc := range []objectGrantBodyCase{
		{
			name: "date_part_timestamp_cast", columns: "d DATE,ts TIMESTAMP", values: "DATE '2001-01-01',TIMESTAMP '2001-01-01'",
			expression: "date_part('year',d)", nullExpression: "date_part('year',NULL::DATE)", deadExpression: "CASE WHEN FALSE THEN date_part('year',d) ELSE 2001::FLOAT8 END",
			resultType: "FLOAT8", value: float64(2001), target: "pg_catalog.timestamp(date)", deniedName: "timestamp",
			primitive: "d::TIMESTAMP = ts", primitiveType: "BOOL", primitiveValue: true,
			independent: "date_part('year',ts)", independentType: "FLOAT8", independentValue: float64(2001),
		},
		{
			// age(timestamp) uses CURRENT_DATE::timestamp as its first body
			// argument. The row is created at this fixture's current midnight;
			// the independently expected age is zero, not a hardcoded date.
			name: "age_current_date_cast", columns: "d DATE,ts TIMESTAMP", values: "CURRENT_DATE,CURRENT_DATE::TIMESTAMP",
			expression: "age(ts) = INTERVAL '0'", nullExpression: "age(NULL::TIMESTAMP) = INTERVAL '0'", deadExpression: "CASE WHEN FALSE THEN age(ts) = INTERVAL '0' ELSE TRUE END",
			resultType: "BOOL", value: true, target: "pg_catalog.timestamp(date)", deniedName: "timestamp",
			primitive: "d::TIMESTAMP = ts", primitiveType: "BOOL", primitiveValue: true,
			independent: "date_part('hour',ts)", independentType: "FLOAT8", independentValue: float64(0),
		},
	} {
		t.Run(tc.name, func(t *testing.T) { objectGrantBodyConsumer(t, tc) })
	}
}

func TestReferenceObjectGrantRoutineBodyCastSleep(t *testing.T) {
	// REL_18_0 pg_sleep_until(timestamptz) subtracts two NUMERIC EXTRACT
	// results, then invokes float8(numeric), OID1746, for pg_sleep(float8).
	// A fixed past deadline and the independent pg_sleep(0) never wait.
	// Pinned body initialization authorizes the function-method conversion
	// even for a source-empty consumer, and denies without Context.
	objectGrantBodyConsumer(t, objectGrantBodyCase{
		name: "sleep_numeric_float8_cast", columns: "ts TIMESTAMPTZ,n NUMERIC", values: "TIMESTAMPTZ '2000-01-01 00:00:00+00',-1",
		expression: "pg_sleep_until(ts)::TEXT", nullExpression: "pg_sleep_until(NULL::TIMESTAMPTZ)::TEXT", deadExpression: "CASE WHEN FALSE THEN pg_sleep_until(ts)::TEXT ELSE '' END",
		resultType: "TEXT", value: "", target: "pg_catalog.float8(numeric)", deniedName: "float8",
		primitive: "n::FLOAT8", primitiveType: "FLOAT8", primitiveValue: float64(-1),
		independent: "pg_sleep(0::FLOAT8)::TEXT", independentType: "TEXT", independentValue: "",
	})
}
