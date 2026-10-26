package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineResultsComposite(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsSetSetup(t, p, b, c)
		resultsCompositeSetup(t, p, b, c)
		for _, name := range []string{"rr_arg", "rr_pos_arg"} {
			resultsBoth(t, p, b, c, "SELECT "+name+"('(4,6)'::rr_pair) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
			resultsBoth(t, p, b, c, "SELECT "+name+"($1::rr_pair) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}, "(4,6)")
			resultsHeld(t, p, b, c, "SELECT "+name+"($1::rr_pair) AS v", []string{"v"}, []string{"INT4"}, [][]any{{"(4,6)"}, {nil}, {"(2,3)"}}, [][][]any{{{int64(10)}}, {{nil}}, {{int64(5)}}})
		}
		resultsBoth(t, p, b, c, "SELECT rr_arg(p=>$1::rr_pair) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}, "(4,6)")
		for _, name := range []string{"rr_named", "rr_whole"} {
			resultsBoth(t, p, b, c, "SELECT "+name+"($1::integer) AS v", []string{"v"}, []string{""}, [][]any{{"(4,8)"}}, 4)
			resultsBoth(t, p, b, c, "SELECT * FROM "+name+"(4)", []string{"key", "val"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(8)}})
			resultsBoth(t, p, b, c, "SELECT ("+name+"(4)).val", []string{"val"}, []string{"INT4"}, [][]any{{int64(8)}})
			resultsBoth(t, p, b, c, "SELECT rr_arg("+name+"(4)) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(12)}})
		}
		resultsExec(t, p, b, c,
			"CREATE DOMAIN rr_pair_domain AS rr_pair CHECK ((VALUE).key>0)",
			"CREATE FUNCTION rr_domain(integer) RETURNS rr_pair_domain LANGUAGE SQL AS 'SELECT ROW($1,$1*2)::rr_pair::rr_pair_domain'",
			"CREATE TYPE rr_other AS (key integer,val integer)")
		resultsBoth(t, p, b, c, "SELECT (rr_domain(4)).key AS key,(rr_domain(4)).val AS val", []string{"key", "val"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(8)}})
		want := fixture.ControlsReferenceDiagnostic("42P13", "return type mismatch in function declared to return rr_pair")
		want.Detail = "Final statement returns rr_other instead of integer at column 1."
		want.Context = `SQL function "rr_wrong_identity"`
		resultsError(t, p, c, "CREATE FUNCTION rr_wrong_identity() RETURNS rr_pair LANGUAGE SQL AS 'SELECT ROW(4,8)::rr_other'", false, "execute", want)
		resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='rr_wrong_identity'", 0)
	})
}

func TestReferenceRoutineResultsEmpty(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsSetSetup(t, p, b, c)
		resultsCompositeSetup(t, p, b, c)
		for _, tc := range []struct {
			q            string
			names, types []string
			rows         [][]any
		}{
			{"SELECT rr_empty(0) AS v", []string{"v"}, []string{"INT4"}, [][]any{{nil}}},
			{"SELECT rr_empty_pair(0) AS v", []string{"v"}, []string{""}, [][]any{{nil}}},
			{"SELECT rr_empty_record(0) AS v", []string{"v"}, []string{"RECORD"}, [][]any{{nil}}},
			{"SELECT rr_null_pair() AS v", []string{"v"}, []string{""}, [][]any{{"(,)"}}},
			{"SELECT rr_null_record() AS v", []string{"v"}, []string{"RECORD"}, [][]any{{"(,)"}}},
			{"SELECT rr_empty_pair(0) IS NOT DISTINCT FROM NULL::rr_pair AS absent,rr_null_pair() IS NOT DISTINCT FROM NULL::rr_pair AS present", []string{"absent", "present"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}},
			{"SELECT rr_empty_record(0) IS NOT DISTINCT FROM NULL::record AS absent,rr_null_record() IS NOT DISTINCT FROM NULL::record AS present", []string{"absent", "present"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}},
			{"SELECT (rr_empty_pair(0)).key,(rr_null_pair()).val", []string{"key", "val"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}}},
			{"SELECT * FROM rr_empty(0)", []string{"rr_empty"}, []string{"INT4"}, [][]any{{nil}}},
			{"SELECT * FROM rr_empty_pair(0)", []string{"key", "val"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}}},
			{"SELECT * FROM rr_empty_record(0)", []string{"key", "val"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}}},
			{"SELECT * FROM rr_set(0)", []string{"rr_set"}, []string{"INT4"}, [][]any{}},
			{"SELECT * FROM rr_table(0)", []string{"key", "val"}, []string{"INT4", "INT4"}, [][]any{}},
			{"SELECT * FROM rr_named_set(0)", []string{"key", "val"}, []string{"INT4", "INT4"}, [][]any{}},
			{"SELECT * FROM rr_record_set(0)", []string{"key", "val"}, []string{"INT4", "INT4"}, [][]any{}},
			{"SELECT rr_empty(2) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}},
			{"SELECT rr_empty_pair(2) AS v", []string{"v"}, []string{""}, [][]any{{"(1,10)"}}},
		} {
			resultsBoth(t, p, b, c, tc.q, tc.names, tc.types, tc.rows)
		}
		resultsHeld(t, p, b, c, "SELECT rr_empty_pair($1::integer) AS v", []string{"v"}, []string{""}, [][]any{{1}, {0}, {2}, {0}, {1}}, [][][]any{{{"(1,10)"}}, {{nil}}, {{"(1,10)"}}, {{nil}}, {{"(1,10)"}}})
		resultsHeld(t, p, b, c, "SELECT rr_empty_record($1::integer) AS v", []string{"v"}, []string{"RECORD"}, [][]any{{1}, {0}, {2}, {1}}, [][][]any{{{"(1,10)"}}, {{nil}}, {{"(1,10)"}}, {{"(1,10)"}}})
	})
}
