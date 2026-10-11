package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOuterJoinsFullExpressions(t *testing.T) {
	fixture.OuterJoinsScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE SEQUENCE outer_key", 0))
		for _, condition := range []string{
			"b.k=a.k", "CAST(a.k AS BIGINT)=b.k", "a.k+1=b.k+1",
			"NOT NOT (a.k=b.k)", "(a.k=b.k) OR (1+1=3)",
			"(a.k=b.k AND a.l='a') OR (a.k=b.k AND b.r='x')",
			"CASE WHEN 1+1=2 THEN a.k=b.k ELSE a.k<b.k END",
		} {
			t.Run(condition, func(t *testing.T) {
				fixture.OuterJoinsCases(t, p, conn, recorder, fixture.OuterJoinsSelect("SELECT * FROM a FULL JOIN b ON "+condition, fixture.OuterJoinsOnRows("FULL")))
			})
		}
		for _, condition := range []string{"(a.k<b.k) AND (2=3)", "NOT (TRUE OR a.k<b.k)", "CASE WHEN 2=3 THEN a.k<b.k ELSE FALSE END"} {
			t.Run(condition, func(t *testing.T) {
				fixture.OuterJoinsCases(t, p, conn, recorder, fixture.OuterJoinsSelect("SELECT * FROM a FULL JOIN b ON "+condition, fixture.OuterJoinsUnmatchedRows("FULL")))
			})
		}
		for _, value := range []any{int64(1), nil} {
			rows := fixture.OuterJoinsOnRows("FULL")
			if value == nil {
				rows = fixture.OuterJoinsUnmatchedRows("FULL")
			}
			step := fixture.OuterJoinsSelect("SELECT * FROM a FULL JOIN b ON a.k=$1 AND a.k=b.k", rows)
			step.Args = []any{value}
			fixture.OuterJoinsCases(t, p, conn, recorder, step)
		}
		for _, value := range []bool{false, true} {
			rows := fixture.OuterJoinsUnmatchedRows("FULL")
			if value {
				rows = fixture.OuterJoinsOnRows("FULL")
			}
			step := fixture.OuterJoinsSelect("SELECT * FROM a FULL JOIN b ON $1::boolean AND a.k=b.k", rows)
			step.Args = []any{value}
			fixture.OuterJoinsCases(t, p, conn, recorder, step)
		}
		for _, condition := range []string{"a.k=1", "a.k+b.k=b.k", "COALESCE((a.k=b.k),FALSE)", "COALESCE(NULL::boolean,(a.k=b.k))", "a.k+nextval('outer_key')=b.k"} {
			t.Run(condition, func(t *testing.T) {
				for _, limit := range []string{"", " LIMIT 0"} {
					step := fixture.ImplicitError("SELECT * FROM a FULL JOIN b ON "+condition+limit, fixture.Diagnostic{Code: "0A000", Message: "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions"})
					step.SelectRows, step.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, conn, recorder, step)
					fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(step))
				}
			})
		}
	})
}
