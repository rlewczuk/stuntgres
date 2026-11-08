package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// arraySubqueryScenario builds one scenario and pins one session per backend.
func arraySubqueryScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, b, c)
		})
	}
}

func arraySubqueryRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

// arraySubqueryBoth runs a SELECT directly and through a fresh Prepare.
// It must never own a DML step: the second route would apply it twice.
func arraySubqueryBoth(run func(fixture.Step), s fixture.Step) {
	run(s)
	run(fixture.Prepared(s))
}
