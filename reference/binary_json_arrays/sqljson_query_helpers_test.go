package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const sqljsonQueryDocument = `{"a":[1,2],"s":"123","o":{"x":1},"n":null,"b":true}`

func sqljsonQueryScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func sqljsonQueryOne(q, typ string, want any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{want}}, args...)
}

func sqljsonQueryCases(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, cases []struct {
	expr, typ string
	want      any
}) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			sqljsonBoth(t, p, b, c, sqljsonQueryOne("SELECT "+tc.expr+" AS v", tc.typ, tc.want))
		})
	}
}
