package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

const jsonpathQueryDocument = `{"a":[1,2,3,4,5],"nil":null,"flag":false}`
const jsonpathQueryPath = `$.a[*]?(@ >= $min && @ <= $max)`
const jsonpathQueryVars = `{"min":2,"max":4}`

var jsonpathQueryFunctions = []struct{ name, typ string }{
	{"jsonb_path_exists", "BOOL"}, {"jsonb_path_match", "BOOL"},
	{"jsonb_path_query", "JSONB"}, {"jsonb_path_query_first", "JSONB"},
}

func jsonpathQueryScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func jsonpathQueryResult(q, typ string, rows [][]any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"changed"}, []string{typ}, rows, args...)
}

func jsonpathQueryOne(q, typ string, value any, args ...any) fixture.Step {
	return jsonpathQueryResult(q, typ, [][]any{{value}}, args...)
}

func jsonpathQueryRows(values ...any) [][]any {
	var rows [][]any
	for _, v := range values {
		rows = append(rows, []any{v})
	}
	return rows
}
