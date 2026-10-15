package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const jsonStripInput = `{"a":1,"b":null,"c":[2,null,3],"d":{"e":4,"f":null}}`
const jsonStripDefault = `{"a":1,"c":[2,null,3],"d":{"e":4}}`
const jsonStripArrays = `{"a":1,"c":[2,3],"d":{"e":4}}`

func jsonStripScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func jsonStripSelect(q string, value any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"stripped"}, []string{"JSON"}, [][]any{{value}}, args...)
}

func jsonStripProcessingError(q, input, code, detail, context string, bound bool) fixture.Step {
	message := "invalid input syntax for type json"
	if code == "22P05" {
		message = "unsupported Unicode escape sequence"
	}
	s := jsonTypeError(q, code, message, 0)
	if bound {
		s.Args = []any{input}
	}
	s.Diagnostic.Detail, s.Diagnostic.Context = detail, context
	return s
}
