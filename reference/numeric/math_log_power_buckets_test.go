package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMathLogPowerBuckets(t *testing.T) {
	mathLogPowerScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		for _, typ := range []string{"numeric", "float8"} {
			for _, tc := range []struct {
				operand, low, high, count string
				want                      int64
			}{
				{"-1", "0", "10", "5", 0}, {"0", "0", "10", "5", 1}, {"2", "0", "10", "5", 2}, {"9", "0", "10", "5", 5}, {"10", "0", "10", "5", 6},
				{"11", "10", "0", "5", 0}, {"10", "10", "0", "5", 1}, {"8", "10", "0", "5", 2}, {"1", "10", "0", "5", 5}, {"0", "10", "0", "5", 6},
				{"0", "0", "1", "2147483647", 1}, {"0.5", "0", "1", "1", 1},
				{"'-Infinity'", "0", "10", "5", 0}, {"'Infinity'", "0", "10", "5", 6},
				{"'Infinity'", "10", "0", "5", 0}, {"'-Infinity'", "10", "0", "5", 6},
			} {
				q := fmt.Sprintf("SELECT width_bucket(%s::%s,%s::%s,%s::%s,%s) AS result", tc.operand, typ, tc.low, typ, tc.high, typ, tc.count)
				mathLogPowerBoth(run, fixture.Select(q, []string{"result"}, []string{"INT4"}, [][]any{{tc.want}}))
			}
		}
		for _, tc := range []struct {
			args string
			want any
		}{
			{"0.58::numeric,0::numeric,1::numeric,50", int64(30)},
			{"9007199254740993::numeric,9007199254740992::numeric,9007199254740994::numeric,2", int64(2)},
			{"1e-1000::numeric,0::numeric,1e-999::numeric,10", int64(2)},
			{"0::float8,(-1e308)::float8,1e308::float8,10", int64(6)},
			{"'0.9999999999999999'::float8,0::float8,1::float8,2147483647", int64(2147483647)},
			{"'5e-324'::float8,0::float8,'1e-323'::float8,2", int64(2)},
			{"1.999999999999999999999999999999::numeric,0::numeric,10::numeric,5", int64(1)},
			{"'1.9999999999999998'::float8,0::float8,10::float8,5", int64(1)},
			{"NULL::numeric,0::numeric,0::numeric,0", nil}, {"1::numeric,NULL::numeric,10::numeric,0", nil},
			{"1::float8,0::float8,NULL::float8,0", nil}, {"1::float8,0::float8,10::float8,NULL::int4", nil},
		} {
			mathLogPowerBoth(run, fixture.Select("SELECT width_bucket("+tc.args+") AS result", []string{"result"}, []string{"INT4"}, [][]any{{tc.want}}))
		}
	})
}

func TestReferenceMathLogPowerBucketDiagnostics(t *testing.T) {
	mathLogPowerScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		for _, typ := range []string{"numeric", "float8"} {
			for _, tc := range []struct{ operand, low, high, count, code, message string }{
				{"1", "0", "10", "0", "2201G", "count must be greater than zero"},
				{"1", "0", "10", "-1", "2201G", "count must be greater than zero"},
				{"1", "0", "10", "-2147483648", "2201G", "count must be greater than zero"},
				{"'NaN'", "0", "10", "0", "2201G", "count must be greater than zero"},
				{"'NaN'", "0", "10", "5", "2201G", "operand, lower bound, and upper bound cannot be NaN"},
				{"1", "'NaN'", "'Infinity'", "5", "2201G", "operand, lower bound, and upper bound cannot be NaN"},
				{"1", "0", "'NaN'", "5", "2201G", "operand, lower bound, and upper bound cannot be NaN"},
				{"1", "'Infinity'", "'Infinity'", "5", "2201G", "lower and upper bounds must be finite"},
				{"1", "'-Infinity'", "10", "5", "2201G", "lower and upper bounds must be finite"},
				{"1", "0", "0", "5", "2201G", "lower bound cannot equal upper bound"},
				{"1", "0", "1", "2147483647", "22003", "integer out of range"},
			} {
				q := fmt.Sprintf("SELECT width_bucket(%s::%s,%s::%s,%s::%s,%s)", tc.operand, typ, tc.low, typ, tc.high, typ, tc.count)
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					run(s)
				}
			}
		}
		mathLogPowerErrors(run, "SELECT width_bucket(1,0,10,2147483648) AS result", fixture.Diagnostic{
			Code: "42883", Message: "function width_bucket(integer, integer, integer, bigint) does not exist",
			Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8",
		}, true)
	})
}

func TestReferenceMathLogPowerThresholds(t *testing.T) {
	mathLogPowerScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		for _, typ := range []string{"integer", "numeric", "float8"} {
			for _, tc := range []struct {
				value string
				want  int64
			}{{"-1", 0}, {"0", 1}, {"9", 1}, {"10", 2}, {"20", 3}, {"21", 3}} {
				q := "SELECT width_bucket(" + tc.value + "::" + typ + ",ARRAY[0,10,20]::" + typ + "[]) AS result"
				mathLogPowerBoth(run, fixture.Select(q, []string{"result"}, []string{"INT4"}, [][]any{{tc.want}}))
			}
		}
		for _, tc := range []struct {
			args string
			want any
		}{
			{"10,ARRAY[0,10,10,20]", int64(3)}, {"1,ARRAY[]::int4[]", int64(0)},
			{"10,'[-2:0]={0,10,20}'::int4[]", int64(2)}, {"5,ARRAY[10,0,20]", int64(2)},
			{"'m'::text,ARRAY['a','m','z']::text[]", int64(2)},
			{"DATE '2024-02-15',ARRAY[DATE '2024-01-01',DATE '2024-02-01',DATE '2024-03-01']", int64(2)},
			{"TIMESTAMP '2024-02-15',ARRAY[TIMESTAMP '2024-01-01',TIMESTAMP '2024-02-01',TIMESTAMP '2024-03-01']", int64(2)},
			{"NULL::int4,ARRAY[NULL::int4]", nil}, {"1,NULL::int4[]", nil},
			{"'NaN'::numeric,ARRAY[0::numeric,'Infinity'::numeric,'NaN'::numeric]", int64(3)},
			{"'NaN'::float8,ARRAY[0::float8,'Infinity'::float8,'NaN'::float8,'NaN'::float8]", int64(4)},
			{"'Infinity'::float8,ARRAY[0::float8,'Infinity'::float8,'NaN'::float8]", int64(2)},
		} {
			mathLogPowerBoth(run, fixture.Select("SELECT width_bucket("+tc.args+") AS result", []string{"result"}, []string{"INT4"}, [][]any{{tc.want}}))
		}
		run(fixture.Exec("CREATE TYPE mlp_rank AS ENUM ('z','a','m')", 0))
		run(fixture.Exec("CREATE DOMAIN mlp_number AS numeric CHECK (VALUE >= 0)", 0))
		run(fixture.Exec("CREATE DOMAIN mlp_thresholds AS mlp_number[]", 0))
		for _, args := range []string{
			"'a'::mlp_rank,ARRAY['z','a','m']::mlp_rank[]",
			"'a'::mlp_rank,ARRAY['z','a','m']::\"_mlp_rank\"",
			"10::mlp_number,ARRAY[0,10,20]::mlp_number[]",
			"10::numeric,ARRAY[0,10,20]::mlp_number[]",
			"10::mlp_number,ARRAY[0,10,20]::mlp_thresholds",
			"10::numeric,ARRAY[0,10,20]::float8[]",
			"'m'::text COLLATE \"C\",ARRAY['a','m','z']::text[]",
		} {
			mathLogPowerBoth(run, fixture.Select("SELECT width_bucket("+args+") AS result", []string{"result"}, []string{"INT4"}, [][]any{{int64(2)}}))
		}
		for _, tc := range []struct {
			args string
			want any
		}{
			{"'a'::mlp_rank,ARRAY[]::mlp_rank[]", int64(0)},
			{"'a'::mlp_rank,NULL::mlp_rank[]", nil},
			{"NULL::mlp_rank,ARRAY['z','a','m']::mlp_rank[]", nil},
		} {
			mathLogPowerBoth(run, fixture.Select("SELECT width_bucket("+tc.args+") AS result", []string{"result"}, []string{"INT4"}, [][]any{{tc.want}}))
		}
		mathLogPowerBoth(run, fixture.Select("SELECT width_bucket($1::mlp_rank,$2::mlp_rank[]) AS result",
			[]string{"result"}, []string{"INT4"}, [][]any{{int64(2)}}, "a", "{z,a,m}"))
	})
}

func TestReferenceMathLogPowerThresholdDiagnostics(t *testing.T) {
	mathLogPowerScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		for _, tc := range []struct{ args, code, message string }{
			{"1,ARRAY[[0,10],[20,30]]", "2202E", "thresholds must be one-dimensional array"},
			{"1,ARRAY[[0,NULL],[20,30]]", "2202E", "thresholds must be one-dimensional array"},
			{"(-1),ARRAY[0,NULL,20]", "22004", "thresholds array must not contain NULLs"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT width_bucket("+tc.args+")", fixture.Diagnostic{Code: tc.code, Message: tc.message})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				run(s)
			}
		}
		for _, query := range []string{"SELECT width_bucket('{}'::json,ARRAY[]::json[])", "SELECT width_bucket('{}'::json,ARRAY['{}'::json])"} {
			mathLogPowerErrors(run, query, fixture.Diagnostic{Code: "42883", Message: "could not identify a comparison function for type json"}, false)
		}
		mathLogPowerBoth(run, fixture.Select("SELECT width_bucket(NULL::json,ARRAY[]::json[])", []string{"width_bucket"}, []string{"INT4"}, [][]any{{nil}}))
		mathLogPowerBoth(run, fixture.Select("SELECT width_bucket('1','{0,1,2}')", []string{"width_bucket"}, []string{"INT4"}, [][]any{{int64(2)}}))
		for _, tc := range []struct{ query, message string }{
			{"SELECT width_bucket(1,ARRAY['a','b']::text[])", "function width_bucket(integer, text[]) does not exist"},
			{"SELECT width_bucket(VARIADIC ARRAY[1,2])", "function width_bucket(integer[]) does not exist"},
		} {
			mathLogPowerErrors(run, tc.query, fixture.Diagnostic{Code: "42883", Message: tc.message,
				Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, true)
		}
	})
}
