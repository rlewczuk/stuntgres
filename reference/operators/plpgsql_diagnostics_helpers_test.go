package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func diagnosticsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

const diagnosticsSnapshot = " GET DIAGNOSTICS n = ROW_COUNT; s := s || n::text || ':' || found::text || ';'; "

func diagnosticsTrace(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, body, want string) {
	t.Helper()
	userOperatorExec(t, p, b, c, "CREATE FUNCTION dg_trace() RETURNS text LANGUAGE plpgsql AS $$DECLARE n bigint; x integer; s text := ''; BEGIN "+body+" RETURN s; END$$")
	sqlExecutionValue(t, p, b, c, "SELECT dg_trace() AS v", "TEXT", want)
}
