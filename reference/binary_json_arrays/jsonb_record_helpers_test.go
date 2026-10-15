package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbRecordScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func jsonbRecordSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, statements ...string) {
	t.Helper()
	for _, q := range statements {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
}

// Named composite OIDs have no database type name. The existing public fixture
// codec helper independently asserts pq []byte versus native string transport,
// including middleware-owned values, rather than weakening generic raw checks.
func jsonbRecordCompositeBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			s.Prepared = prepared
			fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}
