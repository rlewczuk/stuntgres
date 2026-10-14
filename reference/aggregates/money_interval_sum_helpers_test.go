package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func moneyIntervalSumScenario(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET lc_monetary='C'", 0))
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET IntervalStyle='postgres'", 0))
			body(t, p, conn, b)
		})
	}
}

func moneyIntervalSumBoth(t *testing.T, p *fixture.Pair, conn fixture.Executor, b fixture.Backend, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
	}
}

func moneyIntervalSumPair(q string, rows [][]any) fixture.Step {
	return fixture.Select(q, []string{"m", "iv"}, []string{"MONEY", "INTERVAL"}, rows)
}

func moneyIntervalSumExecutionError(t *testing.T, p *fixture.Pair, conn fixture.Executor, b fixture.Backend, q, code, message string, query bool) {
	t.Helper()
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
	s.SelectRows, s.ErrorStage = query, "execute"
	moneyIntervalSumBoth(t, p, conn, b, s)
}
