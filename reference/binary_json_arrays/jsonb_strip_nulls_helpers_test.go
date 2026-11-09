package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const jsonbStripInput = `{"a":1,"b":null,"c":[2,null,3],"d":{"e":4,"f":null}}`
const jsonbStripDefault = `{"a": 1, "c": [2, null, 3], "d": {"e": 4}}`
const jsonbStripArrays = `{"a": 1, "c": [2, 3], "d": {"e": 4}}`

func jsonbStripScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func jsonbStripSelect(q string, value any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"stripped"}, []string{"JSONB"}, [][]any{{value}}, args...)
}
