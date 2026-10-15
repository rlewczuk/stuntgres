package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbEachScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			body(t, p, backend, conn)
		})
	}
}

func jsonbEachType(function string) string {
	if function == "jsonb_each_text" {
		return "TEXT"
	}
	return "JSONB"
}

func jsonbEachSelect(function, query string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, []string{"key", "value"}, []string{"TEXT", jsonbEachType(function)}, rows, args...)
	s.Bag = true
	return s
}
