package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations independently specify the pinned PostgreSQL 18 contract.
func jsonElementsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func jsonElementsType(f string) string {
	if f == "json_array_elements_text" {
		return "TEXT"
	}
	return "JSON"
}

func jsonElementsSelect(f, query string, rows [][]any, args ...any) fixture.Step {
	return fixture.Select(query, []string{"value"}, []string{jsonElementsType(f)}, rows, args...)
}

func jsonElementsMetadata(s fixture.Step, pg bool) fixture.Step {
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		jsonEachMetadata(t, rows, pg, s.Columns, s.Types)
	}
	return s
}
