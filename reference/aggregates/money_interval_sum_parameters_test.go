package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMoneyIntervalSumParameters(t *testing.T) {
	moneyIntervalSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mis_parameters(id INTEGER,m MONEY,iv INTERVAL)", 0))
		run(fixture.Exec("INSERT INTO mis_parameters VALUES (1,'1','1 day'),(2,'2','2 days')", 2))
		q := "SELECT sum(CASE WHEN id=1 THEN $1::money ELSE m END ORDER BY id) AS m,sum(CASE WHEN id=1 THEN $2::interval ELSE iv END ORDER BY id) AS iv FROM mis_parameters"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		for _, c := range []struct {
			m, iv                  any
			rows                   [][]any
			code, message, context string
		}{
			{"12.34", "1 month", [][]any{{"$14.34", "1 mon 2 days"}}, "", "", ""},
			{nil, nil, [][]any{{"$2.00", "2 days"}}, "", "", ""},
			{"92233720368547758.07", "1 day", nil, "22003", "money out of range", ""},
			{"1", "2147483647 days", nil, "22008", "interval out of range", ""},
			{"oops", "1 day", nil, "22P02", "invalid input syntax for type money: \"oops\"", "unnamed portal parameter $1 = '...'"},
			{"1", "oops", nil, "22007", "invalid input syntax for type interval: \"oops\"", "unnamed portal parameter $2 = '...'"},
			{"12.34", "1 month", [][]any{{"$14.34", "1 mon 2 days"}}, "", "", ""},
		} {
			var s fixture.Step
			if c.code == "" {
				s = moneyIntervalSumPair(q, c.rows)
				s.Args = []any{c.m, c.iv}
			} else {
				s = fixture.ImplicitError(q, fixture.Diagnostic{Code: c.code, Message: c.message, Context: c.context}, c.m, c.iv)
				s.SelectRows, s.ErrorStage = true, "execute"
			}
			moneyIntervalSumBoth(t, p, conn, b, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		// Repeated positional parameters retain the same typed input family.
		moneyIntervalSumBoth(t, p, conn, b, moneyIntervalSumWithArgs(moneyIntervalSumPair("SELECT sum($1::money) AS m,sum($2::interval) AS iv FROM mis_parameters WHERE $1::money='1'::money AND $2::interval='1 day'::interval", [][]any{{"$2.00", "2 days"}}), "1", "1 day"))
	})
}

func moneyIntervalSumWithArgs(s fixture.Step, args ...any) fixture.Step { s.Args = args; return s }
