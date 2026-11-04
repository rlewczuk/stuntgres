package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIntarraySetFeatureSmoke(t *testing.T) {
	intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		intarraySetSeed(t, p, b, c)
		intarrayBoth(t, p, b, c, intarraySetStoredCheck(b))
		// Every documented signature is selected explicitly, both by its operator
		// and by its genuine function, with independent BOOL/array transport.
		for _, tc := range intarraySetCases {
			for _, named := range []bool{false, true} {
				q := "SELECT " + tc.expression("a", intarraySetLiteral(tc.right, tc.rightType), named) + " AS v FROM ia_set_rows WHERE id=1"
				intarrayBoth(t, p, b, c, intarraySetResult(b, q, tc.family, tc.want))
			}
		}
		q := "SELECT a OPERATOR(ia_provider.+) $1::integer[] AS v FROM ia_set_rows WHERE id=1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, intarraySetResult(b, q, "_INT4", "{3,1,3,2,2,4,2}", "{2,4,2}"))
		bad := fixture.ErrorDiagnostic(q, intarraySetNullError(), "{NULL}")
		bad.SelectRows, bad.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
		intarrayBoth(t, p, b, c, intarraySetStoredCheck(b))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, intarraySetResult(b, q, "_INT4", "{3,1,3,2,9}", []byte("{9}")))
		for _, tc := range []struct {
			op   string
			want bool
		}{{"&&", false}, {"@>", true}, {"<@", false}} {
			intarrayBoth(t, p, b, c, intarraySetResult(b, "SELECT '{1,1}'::integer[] OPERATOR(ia_provider."+tc.op+") '{}'::integer[] AS v", "BOOL", tc.want))
		}
		intarraySetPresentation(t, p, b, c)
		if !b.PG {
			intarraySetNativeRoundtrip(t, p, b, c)
		}
	})
}
