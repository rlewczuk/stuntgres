package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferencePGNotifyCTE shows that both CTE evaluation shapes agree with a
// direct call: one VOID row and exactly one delivered event per statement.
func TestReferencePGNotifyCTE(t *testing.T) {
	cases := []struct{ name, query, payload string }{
		{"inlined", "WITH n AS (SELECT pg_notify('cte','inlined')) SELECT * FROM n", "inlined"},
		{"materialized", "WITH n AS MATERIALIZED (SELECT pg_notify('cte','materialized')) SELECT * FROM n", "materialized"},
		{"outer-call", "WITH n AS (SELECT 1 AS v) SELECT pg_notify('cte','outer') FROM n", "outer"},
	}
	pgNotifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		r := pgNotifyConn(t, p, b)
		s, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		pid := uint64(pgNotifyPID(t, p, s))
		pgNotifySQL(t, p, b, r, "LISTEN cte")
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Select(
					c.query, []string{"pg_notify"}, []string{"VOID"}, [][]any{{""}},
				))
				pgNotifyReceive(t, p, b, r, "cte", c.payload, pid)
			})
		}
		pgNotifyAbsent(t, p, b, r)
	})
}
