package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDiscardGrammarAndUtilityPhases(t *testing.T) {
	discardScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		for _, category := range []string{"ALL", "PLANS", "SEQUENCES", "TEMP", "TEMPORARY"} {
			t.Run(category, func(t *testing.T) {
				discardExec(t, p, b, a, "PREPARE p AS SELECT 7 AS v")
				discardExec(t, p, b, a, "CREATE TEMP TABLE discard_prepare(v int)")
				stmt, err := a.PrepareContext(p.Ctx, "DISCARD "+category)
				if err != nil {
					t.Fatal(err)
				}
				discardScalar(t, p, b, a, "EXECUTE p", "v", "INT4", int64(7))
				sqlPreparedStep(t, p, b, a, fixture.Select("SELECT v FROM discard_prepare", []string{"v"}, []string{"INT4"}, [][]any{}))
				discardExec(t, p, b, fixture.HeldStatement{Executor: a, Statement: stmt, SQL: "DISCARD " + category}, "DISCARD "+category)
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
				discardExec(t, p, b, a, "DISCARD "+category)
				sqlPreparedStep(t, p, b, a, fixture.Select("DISCARD "+category, []string{}, []string{}, [][]any{}))
				discardExec(t, p, b, a, "DEALLOCATE ALL")
				discardExec(t, p, b, a, "DROP TABLE IF EXISTS discard_prepare")
			})
		}
		cases := []struct{ q, token string }{
			{"DISCARD", ""}, {"DISCARD p", "p"}, {"DISCARD TEMP p", "p"},
			{"DISCARD IF EXISTS TEMP", "IF"}, {"DISCARD TEMP, PLANS", ","},
			{"DISCARD ALL PLANS", "PLANS"}, {"DISCARD PLANS SELECT 1", "SELECT"},
			{"\"DISCARD\" ALL", "\"DISCARD\""}, {"DISCARD \"TEMP\"", "\"TEMP\""},
		}
		for _, tc := range cases {
			for _, prepared := range []bool{false, true} {
				message := "syntax error at end of input"
				position := "8"
				if tc.token != "" {
					message = "syntax error at or near \"" + tc.token + "\""
					position = sqlPreparedPosition(tc.q, tc.token)
				}
				d := sqlPreparedDiagnostic("42601", message)
				d.Position = position
				s := fixture.ErrorDiagnostic(tc.q, d)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				sqlPreparedStep(t, p, b, a, s)
			}
		}
	})
}
