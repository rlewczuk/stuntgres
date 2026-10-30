package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func hstoreRecordScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, backend, c)
		})
	}
}

func hstoreRecordSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hstoreInstall(t, p, b, c)
	hstoreRun(t, p, b, c, fixture.Exec("CREATE TYPE hs_record_pair AS (i integer,n text)", 0))
}

func hstoreRecordText(q string, value any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{value}}, args...)
}

func hstoreRecordBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	hstoreRun(t, p, b, c, s)
	hstoreRun(t, p, b, c, fixture.Prepared(s))
}

func hstoreRecordCall(operator bool, base, source string) string {
	if operator {
		return "(" + base + " #= " + source + ")"
	}
	return "populate_record(" + base + "," + source + ")"
}
