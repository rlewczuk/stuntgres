package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryOuterJoinsFullExpressions(t *testing.T) {
	outerJoinsScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE SEQUENCE outer_key", 0))
		for _, condition := range []string{
			"b.k=a.k", "CAST(a.k AS BIGINT)=b.k", "a.k+1=b.k+1",
			"NOT NOT (a.k=b.k)", "(a.k=b.k) OR (1+1=3)",
			"(a.k=b.k AND a.l='a') OR (a.k=b.k AND b.r='x')",
			"CASE WHEN 1+1=2 THEN a.k=b.k ELSE a.k<b.k END",
		} {
			t.Run(condition, func(t *testing.T) {
				outerJoinsCases(t, p, conn, recorder, outerJoinsSelect("SELECT * FROM a FULL JOIN b ON "+condition, outerJoinsOnRows("FULL")))
			})
		}
		for _, condition := range []string{"(a.k<b.k) AND (2=3)", "NOT (TRUE OR a.k<b.k)", "CASE WHEN 2=3 THEN a.k<b.k ELSE FALSE END"} {
			t.Run(condition, func(t *testing.T) {
				outerJoinsCases(t, p, conn, recorder, outerJoinsSelect("SELECT * FROM a FULL JOIN b ON "+condition, outerJoinsUnmatchedRows("FULL")))
			})
		}
		for _, value := range []any{int64(1), nil} {
			rows := outerJoinsOnRows("FULL")
			if value == nil {
				rows = outerJoinsUnmatchedRows("FULL")
			}
			step := outerJoinsSelect("SELECT * FROM a FULL JOIN b ON a.k=$1 AND a.k=b.k", rows)
			step.args = []any{value}
			outerJoinsCases(t, p, conn, recorder, step)
		}
		for _, value := range []bool{false, true} {
			rows := outerJoinsUnmatchedRows("FULL")
			if value {
				rows = outerJoinsOnRows("FULL")
			}
			step := outerJoinsSelect("SELECT * FROM a FULL JOIN b ON $1::boolean AND a.k=b.k", rows)
			step.args = []any{value}
			outerJoinsCases(t, p, conn, recorder, step)
		}
		for _, condition := range []string{"a.k=1", "a.k+b.k=b.k", "COALESCE((a.k=b.k),FALSE)", "COALESCE(NULL::boolean,(a.k=b.k))", "a.k+nextval('outer_key')=b.k"} {
			t.Run(condition, func(t *testing.T) {
				for _, limit := range []string{"", " LIMIT 0"} {
					step := referenceImplicitError("SELECT * FROM a FULL JOIN b ON "+condition+limit, referenceDiagnostic{Code: "0A000", Message: "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions"})
					step.selectRows, step.errorStage = true, "execute"
					referenceRunStep(t, p.ctx, conn, recorder, step)
					referenceRunStep(t, p.ctx, conn, recorder, referencePrepared(step))
				}
			})
		}
	})
}
