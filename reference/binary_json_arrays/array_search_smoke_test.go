package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArraySearchFeatureSmoke(t *testing.T) {
	arraySearchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT array_position($1::int[],7) AS p,array_position($1::int[],7,-1) AS start,array_positions($1::int[],7) AS ps,array_positions($1::int[],NULL) AS ns,array_remove($1::int[],7) AS rm,array_replace($1::int[],NULL,9) AS rp"
		arraySearchBoth(t, p, b, c, arraySearchSelect(b, q, []string{"p", "start", "ps", "ns", "rm", "rp"}, []string{"INT4", "INT4", "_INT4", "_INT4", "_INT4", "_INT4"}, [][]any{{int64(-2), int64(0), "{-2,0,2}", "{-1}", "[-2:-1]={NULL,8}", "[-2:2]={7,9,7,8,7}"}}, arraySearchInput))
		arraySearchBoth(t, p, b, c, fixture.Select("SELECT ARRAY[1] @> ARRAY[1,1] AS contains,ARRAY[1,1] <@ ARRAY[1] AS contained,ARRAY[[1,2],[3,4]] && ARRAY[4] AS overlap,ARRAY[NULL]::int[] && ARRAY[NULL]::int[] AS null_overlap", []string{"contains", "contained", "overlap", "null_overlap"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, false}}))
		arraySearchBoth(t, p, b, c, arraySearchSelect(b, "SELECT array_replace(ARRAY[[1,NULL],[1,2]],1,NULL) AS rp,array_position(NULL::int[],1) AS p,array_positions(NULL::int[],1) AS ps,array_positions('{}'::int[],1) AS empty", []string{"rp", "p", "ps", "empty"}, []string{"_INT4", "INT4", "_INT4", "_INT4"}, [][]any{{"{{NULL,NULL},{NULL,2}}", nil, nil, "{}"}}))
		for _, f := range []string{"array_position", "array_positions", "array_remove"} {
			message := "searching for elements in multidimensional arrays is not supported"
			if f == "array_remove" {
				message = "removing elements from multidimensional arrays is not supported"
			}
			arraySearchBoth(t, p, b, c, arraySearchRuntimeError("SELECT "+f+"($1::int[],1)", "0A000", message, "{{1}}"))
		}
		const heldSQL = "SELECT array_position($1::int[],$2::int,$3::int) AS p,array_positions($1::int[],$2::int) AS ps,array_remove($1::int[],$2::int) AS rm"
		stmt, err := c.PrepareContext(p.Ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: heldSQL}
		for _, tc := range []struct{ args, want []any }{
			{[]any{arraySearchInput, 7, -1}, []any{int64(0), "{-2,0,2}", "[-2:-1]={NULL,8}"}},
			{[]any{arraySearchInput, nil, -2}, []any{int64(-1), "{-1}", "[-2:1]={7,7,8,7}"}},
			{[]any{"{}", 7, nil}, []any{nil, "{}", "{}"}},
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, arraySearchSelect(b, heldSQL, []string{"p", "ps", "rm"}, []string{"INT4", "_INT4", "_INT4"}, [][]any{tc.want}, tc.args...))
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, arraySearchRuntimeError(heldSQL, "22004", "initial position must not be null", "{NULL}", nil, nil))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, arraySearchSelect(b, heldSQL, []string{"p", "ps", "rm"}, []string{"INT4", "_INT4", "_INT4"}, [][]any{{int64(-2), "{-2,0,2}", "[-2:-1]={NULL,8}"}}, arraySearchInput, 7, -2))
		check := arraySearchStoredSetup(t, p, b, c)
		arraySearchBoth(t, p, b, c, check)
		if !b.PG {
			fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check)
		}
	})
}
