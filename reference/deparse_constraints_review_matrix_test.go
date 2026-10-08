//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

// Goldens are independent pinned PostgreSQL 18.0 observations. Internal
// "char" columns and regclass DEFAULT are excluded native capabilities;
// regclass providers are exercised through sequence calls instead.
var deparseReviewChecks = []struct{ name, input, compact, pretty string }{
	{"nested_c", "pg_get_constraintdef(0) IS NULL", "CHECK ((pg_get_constraintdef((0)::oid) IS NULL))", "CHECK (pg_get_constraintdef(0::oid) IS NULL)"},
	{"nested_e", "pg_get_expr(NULL,0) IS NULL", "CHECK ((pg_get_expr(NULL::pg_node_tree, (0)::oid) IS NULL))", "CHECK (pg_get_expr(NULL::pg_node_tree, 0::oid) IS NULL)"},
	{"cov_col", "col_description(a,1) IS NULL", "CHECK ((col_description((a)::oid, 1) IS NULL))", "CHECK (col_description(a::oid, 1) IS NULL)"},
	{"cov_curr", "currval('dc_cov_s')>0", "CHECK ((currval('dc_cov_s'::regclass) > 0))", "CHECK (currval('dc_cov_s'::regclass) > 0)"},
	{"cov_last", "lastval()>0", "CHECK ((lastval() > 0))", "CHECK (lastval() > 0)"},
	{"cov_next", "nextval('dc_cov_s')>0", "CHECK ((nextval('dc_cov_s'::regclass) > 0))", "CHECK (nextval('dc_cov_s'::regclass) > 0)"},
	{"cov_nonnulls", "num_nonnulls(a,b,NULL)>0", "CHECK ((num_nonnulls(a, b, NULL::unknown) > 0))", "CHECK (num_nonnulls(a, b, NULL::unknown) > 0)"},
	{"cov_nulls", "num_nulls(a,b,NULL)>0", "CHECK ((num_nulls(a, b, NULL::unknown) > 0))", "CHECK (num_nulls(a, b, NULL::unknown) > 0)"},
	{"cov_obj", "obj_description(a,'pg_class') IS NULL", "CHECK ((obj_description((a)::oid, 'pg_class'::name) IS NULL))", "CHECK (obj_description(a::oid, 'pg_class'::name) IS NULL)"},
	{"cov_obj_legacy", "obj_description(a) IS NULL", "CHECK ((obj_description((a)::oid) IS NULL))", "CHECK (obj_description(a::oid) IS NULL)"},
	{"cov_schemas_false", "current_schemas(false) IS NOT NULL", "CHECK ((current_schemas(false) IS NOT NULL))", "CHECK (current_schemas(false) IS NOT NULL)"},
	{"cov_schemas_true", "current_schemas(true) IS NOT NULL", "CHECK ((current_schemas(true) IS NOT NULL))", "CHECK (current_schemas(true) IS NOT NULL)"},
	{"cov_set2", "setval('dc_cov_s',1)>0", "CHECK ((setval('dc_cov_s'::regclass, (1)::bigint) > 0))", "CHECK (setval('dc_cov_s'::regclass, 1::bigint) > 0)"},
	{"cov_set3false", "setval('dc_cov_s',1,false)>0", "CHECK ((setval('dc_cov_s'::regclass, (1)::bigint, false) > 0))", "CHECK (setval('dc_cov_s'::regclass, 1::bigint, false) > 0)"},
	{"cov_set3true", "setval('dc_cov_s',1,true)>0", "CHECK ((setval('dc_cov_s'::regclass, (1)::bigint, true) > 0))", "CHECK (setval('dc_cov_s'::regclass, 1::bigint, true) > 0)"},
	{"cov_shared", "shobj_description(a,'pg_authid') IS NULL", "CHECK ((shobj_description((a)::oid, 'pg_authid'::name) IS NULL))", "CHECK (shobj_description(a::oid, 'pg_authid'::name) IS NULL)"},
	{"cov_compare_between", "a BETWEEN 1 AND 3 = TRUE", "CHECK ((((a >= 1) AND (a <= 3)) = true))", "CHECK ((a >= 1 AND a <= 3) = true)"},
	{"cov_distinct_endpoint", "TRUE BETWEEN (a IS DISTINCT FROM 1) AND TRUE", "CHECK (((true >= (a IS DISTINCT FROM 1)) AND (true <= true)))", "CHECK (true >= (a IS DISTINCT FROM 1) AND true <= true)"},
	{"cov_endpoint", "TRUE BETWEEN (a<2) AND TRUE", "CHECK (((true >= (a < 2)) AND (true <= true)))", "CHECK (true >= (a < 2) AND true <= true)"},
	{"cov_logic_endpoint", "TRUE BETWEEN (a>0 AND flag) AND TRUE", "CHECK (((true >= ((a > 0) AND flag)) AND (true <= true)))", "CHECK (true >= (a > 0 AND flag) AND true <= true)"},
	{"cov_null_endpoint", "TRUE BETWEEN (a IS NULL) AND TRUE", "CHECK (((true >= (a IS NULL)) AND (true <= true)))", "CHECK (true >= (a IS NULL) AND true <= true)"},
	{"cov_truth_endpoint", "TRUE BETWEEN (flag IS TRUE) AND TRUE", "CHECK (((true >= (flag IS TRUE)) AND (true <= true)))", "CHECK (true >= (flag IS TRUE) AND true <= true)"},
	{"cov_interval3", "INTERVAL(3) '1.2345 seconds' IS NOT NULL", "CHECK (('00:00:01.235'::interval(3) IS NOT NULL))", "CHECK ('00:00:01.235'::interval(3) IS NOT NULL)"},
	{"cov_interval_fields", "INTERVAL '1 day 02:03:04.567' DAY TO SECOND(2) IS NOT NULL", "CHECK (('1 day 02:03:04.57'::interval day to second(2) IS NOT NULL))", "CHECK ('1 day 02:03:04.57'::interval day to second(2) IS NOT NULL)"},
	{"cov_interval_zone", "TIMESTAMP '2026-10-08 00:00:00' AT TIME ZONE INTERVAL '02:00' HOUR TO MINUTE IS NOT NULL", "CHECK ((('2026-10-08 00:00:00'::timestamp without time zone AT TIME ZONE '02:00:00'::interval hour to minute) IS NOT NULL))", "CHECK (('2026-10-08 00:00:00'::timestamp without time zone AT TIME ZONE '02:00:00'::interval hour to minute) IS NOT NULL)"},
	{"cov_q_local", "TIMETZ '12:34:56+00' AT LOCAL IS NOT NULL", "CHECK ((('12:34:56+00'::time with time zone AT LOCAL) IS NOT NULL))", "CHECK (('12:34:56+00'::time with time zone AT LOCAL) IS NOT NULL)"},
	{"cov_q_zone", "TIMETZ '12:34:56+00' AT TIME ZONE 'UTC' IS NOT NULL", "CHECK ((('12:34:56+00'::time with time zone AT TIME ZONE 'UTC'::text) IS NOT NULL))", "CHECK (('12:34:56+00'::time with time zone AT TIME ZONE 'UTC'::text) IS NOT NULL)"},
	{"cov_time3", "TIME(3) '12:34:56.1234' IS NOT NULL", "CHECK (('12:34:56.1234'::time(3) without time zone IS NOT NULL))", "CHECK ('12:34:56.1234'::time(3) without time zone IS NOT NULL)"},
	{"cov_timestamp3", "TIMESTAMP(3) '2026-10-08 12:34:56.1234' IS NOT NULL", "CHECK (('2026-10-08 12:34:56.1234'::timestamp(3) without time zone IS NOT NULL))", "CHECK ('2026-10-08 12:34:56.1234'::timestamp(3) without time zone IS NOT NULL)"},
	{"cov_timestamptz3", "TIMESTAMP(3) WITH TIME ZONE '2026-10-08 12:34:56.1234+00' IS NOT NULL", "CHECK (('2026-10-08 12:34:56.1234+00'::timestamp(3) with time zone IS NOT NULL))", "CHECK ('2026-10-08 12:34:56.1234+00'::timestamp(3) with time zone IS NOT NULL)"},
	{"cov_timetz3", "TIME(3) WITH TIME ZONE '12:34:56.1234+00' IS NOT NULL", "CHECK (('12:34:56.1234+00'::time(3) with time zone IS NOT NULL))", "CHECK ('12:34:56.1234+00'::time(3) with time zone IS NOT NULL)"},
	{"cov_ts_local", "TIMESTAMP '2026-10-08 00:00:00' AT LOCAL IS NOT NULL", "CHECK ((('2026-10-08 00:00:00'::timestamp without time zone AT LOCAL) IS NOT NULL))", "CHECK (('2026-10-08 00:00:00'::timestamp without time zone AT LOCAL) IS NOT NULL)"},
	{"cov_ts_zone", "TIMESTAMP '2026-10-08 00:00:00' AT TIME ZONE 'UTC' IS NOT NULL", "CHECK ((('2026-10-08 00:00:00'::timestamp without time zone AT TIME ZONE 'UTC'::text) IS NOT NULL))", "CHECK (('2026-10-08 00:00:00'::timestamp without time zone AT TIME ZONE 'UTC'::text) IS NOT NULL)"},
	{"cov_tz_local", "TIMESTAMPTZ '2026-10-08 00:00:00+00' AT LOCAL IS NOT NULL", "CHECK ((('2026-10-08 00:00:00+00'::timestamp with time zone AT LOCAL) IS NOT NULL))", "CHECK (('2026-10-08 00:00:00+00'::timestamp with time zone AT LOCAL) IS NOT NULL)"},
	{"cov_tz_zone", "TIMESTAMPTZ '2026-10-08 00:00:00+00' AT TIME ZONE 'UTC' IS NOT NULL", "CHECK ((('2026-10-08 00:00:00+00'::timestamp with time zone AT TIME ZONE 'UTC'::text) IS NOT NULL))", "CHECK (('2026-10-08 00:00:00+00'::timestamp with time zone AT TIME ZONE 'UTC'::text) IS NOT NULL)"},
}

var deparseReviewDefaults = []struct{ name, declaration, input, golden, value string }{
	{"bool_v", "BOOL", "TRUE", "true", "true"},
	{"int4_negative", "INT", "-1", "'-1'::integer", "-1"},
	{"int2_unknown", "SMALLINT", "'1'", "'1'::smallint", "1"},
	{"int8_unknown", "BIGINT", "'1'", "'1'::bigint", "1"},
	{"int8_operator", "BIGINT", "1", "1", "1"},
	{"oid_unknown", "OID", "'1'", "'1'::oid", "1"},
	{"text_v", "TEXT", "'a''b'", "'a''b'::text", "a'b"},
	{"name_v", "NAME", "'hello'", "'hello'::name", "hello"},
	{"bpchar_v", "BPCHAR", "'hi'", "'hi'::bpchar", "hi"},
	{"bpchar3", "CHARACTER(3)", "'xy'", "'xy'::bpchar", "xy"},
	{"varchar_v", "VARCHAR", "'hi'", "'hi'::character varying", "hi"},
	{"varchar3", "VARCHAR(3)", "'xy'", "'xy'::character varying", "xy"},
	{"bytea_v", "BYTEA", "'\\x00ff'", "'\\x00ff'::bytea", "\\x00ff"},
	{"uuid_v", "UUID", "'00000000-0000-0000-0000-000000000001'", "'00000000-0000-0000-0000-000000000001'::uuid", "00000000-0000-0000-0000-000000000001"},
	{"numeric_decimal", "NUMERIC", "'1.20'", "1.20", "1.20"},
	{"numeric_integer", "NUMERIC", "'1'", "'1'::numeric", "1"},
	{"numeric_negative", "NUMERIC", "'-1.20'", "'-1.20'::numeric", "-1.20"},
	{"numeric_nan", "NUMERIC", "'NaN'", "'NaN'::numeric", "NaN"},
	{"numeric_inf", "NUMERIC", "'Infinity'", "'Infinity'::numeric", "Infinity"},
	{"numeric_ninf", "NUMERIC", "'-Infinity'", "'-Infinity'::numeric", "-Infinity"},
	{"numeric42", "NUMERIC(4,2)", "'1.2'", "1.2", "1.20"},
	{"float4_finite", "REAL", "'1.5'", "'1.5'::real", "1.5"},
	{"float4_nan", "REAL", "'NaN'", "'NaN'::real", "NaN"},
	{"float4_inf", "REAL", "'Infinity'", "'Infinity'::real", "Infinity"},
	{"float4_ninf", "REAL", "'-Infinity'", "'-Infinity'::real", "-Infinity"},
	{"float8_finite", "DOUBLE PRECISION", "'1.5'", "'1.5'::double precision", "1.5"},
	{"float8_nan", "DOUBLE PRECISION", "'NaN'", "'NaN'::double precision", "NaN"},
	{"float8_inf", "DOUBLE PRECISION", "'Infinity'", "'Infinity'::double precision", "Infinity"},
	{"float8_ninf", "DOUBLE PRECISION", "'-Infinity'", "'-Infinity'::double precision", "-Infinity"},
	{"float4_assignment", "REAL", "1", "1", "1"},
	{"float8_assignment", "DOUBLE PRECISION", "0.5", "0.5", "0.5"},
	{"date_v", "DATE", "'2026-10-08'", "'2026-10-08'::date", "2026-10-08"},
	{"date_inf", "DATE", "'infinity'", "'infinity'::date", "infinity"},
	{"date_ninf", "DATE", "'-infinity'", "'-infinity'::date", "-infinity"},
	{"time_v", "TIME", "'12:34:56'", "'12:34:56'::time without time zone", "12:34:56"},
	{"time3", "TIME(3)", "'12:34:56.1234'", "'12:34:56.1234'::time without time zone", "12:34:56.123"},
	{"timetz_v", "TIMETZ", "'12:34:56+00'", "'12:34:56+00'::time with time zone", "12:34:56+00"},
	{"timetz3", "TIMETZ(3)", "'12:34:56.1234+00'", "'12:34:56.1234+00'::time with time zone", "12:34:56.123+00"},
	{"timestamp_v", "TIMESTAMP", "'2026-10-08 12:34:56'", "'2026-10-08 12:34:56'::timestamp without time zone", "2026-10-08 12:34:56"},
	{"timestamp3", "TIMESTAMP(3)", "'2026-10-08 12:34:56.1234'", "'2026-10-08 12:34:56.1234'::timestamp without time zone", "2026-10-08 12:34:56.123"},
	{"timestamp_inf", "TIMESTAMP", "'infinity'", "'infinity'::timestamp without time zone", "infinity"},
	{"timestamp_ninf", "TIMESTAMP", "'-infinity'", "'-infinity'::timestamp without time zone", "-infinity"},
	{"timestamptz_v", "TIMESTAMPTZ", "'2026-10-08 12:34:56+00'", "'2026-10-08 12:34:56+00'::timestamp with time zone", "2026-10-08 12:34:56+00"},
	{"timestamptz3", "TIMESTAMPTZ(3)", "'2026-10-08 12:34:56.1234+00'", "'2026-10-08 12:34:56.1234+00'::timestamp with time zone", "2026-10-08 12:34:56.123+00"},
	{"timestamptz_inf", "TIMESTAMPTZ", "'infinity'", "'infinity'::timestamp with time zone", "infinity"},
	{"timestamptz_ninf", "TIMESTAMPTZ", "'-infinity'", "'-infinity'::timestamp with time zone", "-infinity"},
	{"interval_v", "INTERVAL", "'1 day'", "'1 day'::interval", "1 day"},
	{"interval3", "INTERVAL(3)", "'1.2345 seconds'", "'00:00:01.235'::interval(3)", "00:00:01.235"},
	{"interval_fields", "INTERVAL DAY TO SECOND(2)", "'1 day 02:03:04.567'", "'1 day 02:03:04.57'::interval day to second(2)", "1 day 02:03:04.57"},
	{"interval_year", "INTERVAL YEAR", "'13 months'", "'1 year'::interval year", "1 year"},
	{"interval_inf", "INTERVAL", "'infinity'", "'infinity'::interval", "infinity"},
	{"interval_ninf", "INTERVAL", "'-infinity'", "'-infinity'::interval", "-infinity"},
}

func deparseReviewStreams(t *testing.T, scenario func(*testing.T, *sql.Conn, bool, func(referenceStep) referenceObserved)) {
	t.Helper()
	p := newReferencePair(t)
	for _, b := range []struct {
		name string
		db   *sql.DB
		rec  *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"memory", p.memory, p.memoryRecord}} {
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			scenario(t, conn, b.name == "postgres", func(s referenceStep) referenceObserved { return referenceRunStep(t, p.ctx, conn, b.rec, s) })
		})
	}
}
func TestReferenceDeparseConstraintsReviewMatrix(t *testing.T) {
	for _, tc := range deparseReviewChecks {
		t.Run(tc.name, func(t *testing.T) {
			deparseReviewStreams(t, func(t *testing.T, _ *sql.Conn, _ bool, run func(referenceStep) referenceObserved) {
				run(referenceExec("SET TIME ZONE 'UTC'", 0))
				run(referenceExec("SET DateStyle TO 'ISO, MDY'", 0))
				run(referenceExec("SET IntervalStyle TO 'postgres'", 0))
				run(referenceExec("CREATE SEQUENCE dc_cov_s START 40", 0))
				run(referenceExec("CREATE TABLE original(a INT,b INT,flag BOOL,CONSTRAINT c CHECK("+tc.input+"))", 0))
				for i, want := range []string{tc.compact, tc.pretty} {
					query := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", i == 1)
					s := referenceSelect(query, []string{"d"}, []string{"TEXT"}, [][]any{{want}})
					observed := run(s)
					run(referencePrepared(s))
					run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(a INT,b INT,flag BOOL,CONSTRAINT copied %s)", i, observed.rows[0][0].(string)), 0))
				}
				run(referenceSelect("SELECT nextval('dc_cov_s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(40)}}))
				for _, name := range []string{"original", "copy0", "copy1"} {
					run(referenceExec("INSERT INTO "+name+" VALUES(1,1,true)", 1))
				}
				switch tc.name {
				case "cov_next":
					run(referenceSelect("SELECT currval('dc_cov_s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(43)}}))
				case "cov_curr", "cov_last":
					run(referenceSelect("SELECT currval('dc_cov_s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(40)}}))
				case "cov_set2", "cov_set3true":
					run(referenceSelect("SELECT nextval('dc_cov_s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}}))
				case "cov_set3false":
					run(referenceSelect("SELECT nextval('dc_cov_s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
				}
			})
		})
	}
}
func TestReferenceDeparseConstraintsReviewDefaultCopies(t *testing.T) {
	for _, tc := range deparseReviewDefaults {
		t.Run(tc.name, func(t *testing.T) {
			deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(referenceStep) referenceObserved) {
				run(referenceExec("SET TIME ZONE 'UTC'", 0))
				run(referenceExec("SET DateStyle TO 'ISO, MDY'", 0))
				run(referenceExec("SET IntervalStyle TO 'postgres'", 0))
				run(referenceExec("CREATE TABLE original(id INT,v "+tc.declaration+" DEFAULT "+tc.input+")", 0))
				for i := 0; i < 2; i++ {
					query := fmt.Sprintf("SELECT pg_get_expr(d.adbin,0,%t) AS d FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='original'", i == 1)
					s := referenceSelect(query, []string{"d"}, []string{"TEXT"}, [][]any{{tc.golden}})
					observed := run(s)
					run(referencePrepared(s))
					run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(id INT,v %s DEFAULT %s)", i, tc.declaration, observed.rows[0][0].(string)), 0))
				}
				for _, name := range []string{"original", "copy0", "copy1"} {
					run(referenceExec("INSERT INTO "+name+"(id) VALUES(1)", 1))
					deparseReviewTypedValue(t, conn, postgres, "SELECT v FROM "+name, tc.declaration, tc.value)
				}
			})
		})
	}
}
