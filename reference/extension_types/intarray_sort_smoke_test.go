package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIntarraySortFeatureSmoke(t *testing.T) {
	intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		intarraySortSeed(t, p, b, c)
		for _, tc := range intarraySortCases {
			args := tc.literals(tc.args)
			args[0] = "a"
			q := "SELECT " + tc.expression(args) + " AS v FROM ia_sort_rows WHERE id=1"
			intarrayBoth(t, p, b, c, intarraySortResult(b, q, tc.want))
		}
		for _, tc := range []struct {
			expression string
			want       any
		}{
			{"ia_provider.uniq('{1,2,2,3,1,1}'::integer[])", "{1,2,3,1}"},
			{"ia_provider.uniq(ia_provider.sort('{1,2,3,2,1}'::integer[]))", "{1,2,3}"},
			{"ia_provider.sort_desc('{2,1,2}'::integer[])", "{2,2,1}"},
			{"ia_provider.sort('[0:2]={3,1,2}'::integer[])", "[0:2]={1,2,3}"},
			{"ia_provider.sort('{}'::integer[],'invalid')", "{}"},
			{"ia_provider.sort('{7}'::integer[],'invalid')", "{7}"},
			{"ia_provider.sort(NULL::integer[])", nil},
			{"ia_provider.sort('{1,NULL}'::integer[],NULL::text)", nil},
		} {
			intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT "+tc.expression+" AS v", tc.want))
		}
		q := "SELECT ia_provider.sort($1::integer[],$2::text) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, intarraySortResult(b, q, "{2,2,1}", "{2,1,2}", "dEsC"))
		bad := fixture.ErrorDiagnostic(q, intarraySortDirectionError(), "{2,1,2}", "invalid")
		bad.SelectRows, bad.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, intarraySortResult(b, q, "{1,2,2}", []byte("{2,1,2}"), "aSc"))
		intarrayError(t, p, b, c, "SELECT ia_provider.sort('{1,NULL}'::integer[],'invalid')", false, "execute", intarraySetNullError())
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		intarrayBoth(t, p, b, c, intarraySortStoredCheck(b))
		intarraySortDefaults(t, p, b, c)
		if !b.PG {
			intarraySortNativeRoundtrip(t, p, b, c)
		} else {
			intarraySortControlVerify(t, p, b, c, "stuntgres")
		}
	})
}
