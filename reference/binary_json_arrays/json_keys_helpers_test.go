package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonKeysScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func jsonKeysSelect(q string, keys []string, args ...any) fixture.Step {
	var rows [][]any
	for _, key := range keys {
		rows = append(rows, []any{key})
	}
	s := fixture.Select(q, []string{"json_object_keys"}, []string{"TEXT"}, rows, args...)
	s.Bag = true
	return s
}
