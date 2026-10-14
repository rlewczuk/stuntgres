package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMoneyIntervalSumAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"mis_reader"})
	moneyIntervalSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE USER mis_reader", 0))
		run(fixture.Exec("CREATE TABLE mis_secret(m MONEY,iv INTERVAL,keep BOOLEAN,sort INTEGER,unused INTEGER)", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO mis_reader", 0))
		}
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		q := "SELECT sum(m ORDER BY sort) FILTER(WHERE keep) AS m,sum(iv ORDER BY sort) FILTER(WHERE keep) AS iv FROM mis_secret"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		denial := func(q string) fixture.Step {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table mis_secret"})
			s.SelectRows, s.ErrorStage = true, "execute"
			return s
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION mis_reader", 0))
		moneyIntervalSumBoth(t, p, conn, b, denial("SELECT sum(m) AS m,sum(iv) AS iv FROM mis_secret WHERE FALSE"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denial(q))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(m,iv) ON mis_secret TO mis_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION mis_reader", 0))
		moneyIntervalSumBoth(t, p, conn, b, moneyIntervalSumPair("SELECT sum(m) AS m,sum(iv) AS iv FROM mis_secret", [][]any{{nil, nil}}))
		for _, q := range []string{
			"SELECT sum(m) FILTER(WHERE keep) FROM mis_secret",
			"SELECT sum(iv ORDER BY sort) FROM mis_secret",
			"SELECT sum(m) FROM mis_secret HAVING sum(unused)>0",
			"SELECT sum(iv) FROM mis_secret ORDER BY sum(unused)",
		} {
			moneyIntervalSumBoth(t, p, conn, b, denial(q))
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(keep,sort) ON mis_secret TO mis_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION mis_reader", 0))
		moneyIntervalSumBoth(t, p, conn, b, moneyIntervalSumPair(q, [][]any{{nil, nil}}))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, moneyIntervalSumPair(q, [][]any{{nil, nil}}))
		moneyIntervalSumBoth(t, p, conn, b, denial("SELECT sum(unused) FROM mis_secret"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("INSERT INTO mis_secret VALUES ('1','1 day',TRUE,2,9),('2','2 days',FALSE,1,8)", 2))
		run(fixture.Exec("SET SESSION AUTHORIZATION mis_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, moneyIntervalSumPair(q, [][]any{{"$1.00", "1 day"}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT(keep) ON mis_secret FROM mis_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION mis_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denial(q))
		moneyIntervalSumBoth(t, p, conn, b, denial(q))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(keep) ON mis_secret TO mis_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION mis_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, moneyIntervalSumPair(q, [][]any{{"$1.00", "1 day"}}))
	})
}

func TestReferenceMoneyIntervalSumWindowBoundary(t *testing.T) {
	moneyIntervalSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE mis_window(id INTEGER,m MONEY,iv INTERVAL)", 0))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("INSERT INTO mis_window VALUES (1,'1','1 day'),(2,'2','2 days')", 2))
		for _, c := range []struct{ column, kind, first, second string }{{"m", "MONEY", "$1.00", "$3.00"}, {"iv", "INTERVAL", "1 day", "3 days"}} {
			q := "SELECT sum(" + c.column + ") OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM mis_window ORDER BY id"
			if b.PG {
				moneyIntervalSumBoth(t, p, conn, b, fixture.Select(q, []string{"sum"}, []string{c.kind}, [][]any{{c.first}, {c.second}}))
			} else {
				fixture.IntervalAvgDiagnostic(t, p, conn, b.Recorder, q, fixture.Diagnostic{Code: "0A000", Message: "window function provider is unsupported", Position: "8"})
			}
		}
		moneyIntervalSumBoth(t, p, conn, b, fixture.Select("SELECT sum(id) OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM mis_window ORDER BY id", []string{"sum"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(3)}}))
	})
}
