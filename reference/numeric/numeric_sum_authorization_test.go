package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNumericSumAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"numeric_sum_reader"})
	numericSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE USER numeric_sum_reader", 0))
		run(fixture.Exec("CREATE TABLE source(v INTEGER,secret INTEGER,keep BOOLEAN)", 0))
		run(fixture.Exec("INSERT INTO source VALUES (1,9,TRUE),(3,8,FALSE)", 2))
		if pg {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO numeric_sum_reader", 0))
		}
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		q := "SELECT sum(v) FROM source"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		run(fixture.Exec("SET SESSION AUTHORIZATION numeric_sum_reader", 0))
		denied := func(q string) {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table source"})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				run(s)
			}
		}
		for _, q := range []string{"SELECT sum(v) FROM source", "SELECT sum(v) FROM source WHERE FALSE", "SELECT sum(v) FILTER(WHERE keep) FROM source", "SELECT sum(v ORDER BY secret) FROM source", "SELECT sum(v) FROM source HAVING sum(secret)>0", "SELECT sum(v) FROM source ORDER BY sum(secret)"} {
			denied(q)
		}
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table source"})
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, recorder, s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		fixture.RunStep(t, p.Ctx, held, recorder, fixture.Select(q, []string{"sum"}, []string{"INT8"}, [][]any{{int64(4)}}))
		run(fixture.Exec("GRANT SELECT(v) ON source TO numeric_sum_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION numeric_sum_reader", 0))
		numericSumBoth(t, p, conn, recorder, fixture.Select(q, []string{"sum"}, []string{"INT8"}, [][]any{{int64(4)}}))
		for _, q := range []string{"SELECT sum(secret) FROM source WHERE FALSE", "SELECT sum(v) FILTER(WHERE keep) FROM source", "SELECT sum(v ORDER BY secret) FROM source", "SELECT sum(v) FROM source HAVING sum(secret)>0", "SELECT sum(v) FROM source ORDER BY sum(secret)"} {
			denied(q)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(keep) ON source TO numeric_sum_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION numeric_sum_reader", 0))
		numericSumBoth(t, p, conn, recorder, fixture.Select("SELECT sum(v) FILTER(WHERE keep) FROM source", []string{"sum"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT(v) ON source FROM numeric_sum_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION numeric_sum_reader", 0))
		fixture.RunStep(t, p.Ctx, held, recorder, s)
	})
}
