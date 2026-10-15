package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowOffsetTypes(t *testing.T) {
	queryWindowOffsetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{"CREATE DOMAIN wo_d AS INTEGER", "CREATE DOMAIN wo_d2 AS INTEGER", "CREATE TYPE wo_m AS ENUM('z','a')", "CREATE TYPE wo_m2 AS ENUM('z','a')"} {
			queryWindowRun(t, p, b, fixture.Exec(q, 0))
		}
		for _, fn := range []string{"lead", "lag"} {
			for _, tc := range []struct {
				args, typ string
				value     any
			}{
				{"NULL,1,NULL", "TEXT", nil}, {"NULL,1,'x'", "TEXT", "x"}, {"1,1,0.5", "NUMERIC", "0.5"},
				{"1::smallint,0,2::bigint", "INT8", int64(1)}, {"'7',0,1", "INT4", int64(7)},
				{"7,1::smallint,9", "INT4", int64(9)}, {"7,'0',9", "INT4", int64(7)}, {"7,NULL,9", "INT4", nil},
				{"'abc'::varchar(3),0,'def'::varchar(3)", "VARCHAR", "abc"},
				{"1.25::numeric(4,2),0,2.50::numeric(4,2)", "NUMERIC", "1.25"},
				{"ARRAY[1,NULL::int],1,ARRAY[2,3]", "_INT4", "{2,3}"}, {"'\\x00ff'::bytea,0,NULL::bytea", "BYTEA", string([]byte{0, 255})},
			} {
				t.Run(fn+"/"+tc.args, func(t *testing.T) {
					queryWindowRoutes(t, p, b, fixture.Select("SELECT "+fn+"("+tc.args+") OVER() AS x", []string{"x"}, []string{tc.typ}, [][]any{{tc.value}}))
				})
			}
			for i, tc := range []struct {
				args, typ string
				value     any
			}{
				{"1::wo_d,0,2::wo_d", "wo_d", "1"},
				{"1::wo_d,0,NULL", "wo_d", "1"},
				{"1::wo_d,0,2", "integer", "1"},
				{"1::wo_d,0,2::wo_d2", "integer", "1"},
				{"1::wo_d", "wo_d", nil},
				{"'z'::wo_m,1,'a'::wo_m", "wo_m", "a"},
			} {
				// Wire metadata flattens domains. The captured output descriptor
				// retains the resolved identity, independently of that transport.
				view := "wo_type_" + fn + "_" + strconv.Itoa(i)
				queryWindowRun(t, p, b, fixture.Exec("CREATE VIEW "+view+" AS SELECT "+fn+"("+tc.args+") OVER() AS x", 0))
				queryWindowRoutes(t, p, b, fixture.Select("SELECT atttypid::regtype::text AS t,atttypid='"+tc.typ+"'::regtype::oid AS same_type FROM pg_attribute WHERE attrelid='"+view+"'::regclass::oid AND attnum=1", []string{"t", "same_type"}, []string{"TEXT", "BOOL"}, [][]any{{tc.typ, true}}))
				queryWindowRoutes(t, p, b, fixture.Select("SELECT x::text AS x FROM "+view, []string{"x"}, []string{"TEXT"}, [][]any{{tc.value}}))
			}
			queryWindowValueDiagnosticRoutes(t, p, b, "SELECT "+fn+"('z'::wo_m,0,'a'::wo_m2) OVER()", fixture.Diagnostic{Code: "42883", Message: "function " + fn + "(wo_m, integer, wo_m2) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, "prepare")
		}
		queryWindowRoutes(t, p, b, fixture.Select("SELECT lead('a',1,'z' COLLATE \"C\") OVER() < 'ä' AS x,lag('a' COLLATE \"C\",1,'z') OVER() < 'ä' AS y", []string{"x", "y"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		queryWindowValueDiagnosticRoutes(t, p, b, `SELECT lead('a' COLLATE "C",0,'b' COLLATE "POSIX") OVER()`, fixture.Diagnostic{Code: "42P21", Message: `collation mismatch between explicit collations "C" and "POSIX"`, Position: "35"}, "prepare")
		queryWindowValueDiagnosticRoutes(t, p, b, "SELECT lead($1,$1,'x'::text) OVER()", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "text versus integer", Position: "16"}, "prepare", "1")
		queryWindowRoutes(t, p, b, fixture.Select("SELECT lead($1,1,$2::int) OVER() AS x,lag(7,$3,9) OVER() AS y", []string{"x", "y"}, []string{"INT4", "INT4"}, [][]any{{int64(12), int64(7)}}, 11, 12, 0))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT 'pg_catalog.lead(anyelement)'::regprocedure::oid::text AS a,'pg_catalog.lead(anyelement,integer)'::regprocedure::oid::text AS b,'pg_catalog.lead(anycompatible,integer,anycompatible)'::regprocedure::oid::text AS c,'pg_catalog.lag(anyelement)'::regprocedure::oid::text AS d,'pg_catalog.lag(anyelement,integer)'::regprocedure::oid::text AS e,'pg_catalog.lag(anycompatible,integer,anycompatible)'::regprocedure::oid::text AS f", []string{"a", "b", "c", "d", "e", "f"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"3109", "3110", "3111", "3106", "3107", "3108"}}))
	})
}
