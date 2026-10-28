package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonEachScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func jsonEachType(f string) string {
	if f == "json_each_text" {
		return "TEXT"
	}
	return "JSON"
}

func jsonEachSelect(f, q string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, []string{"key", "value"}, []string{"TEXT", jsonEachType(f)}, rows, args...)
	s.Bag = true
	return s
}
