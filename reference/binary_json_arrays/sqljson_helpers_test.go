package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func sqljsonScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func sqljsonValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, cases []struct {
	expr, typ string
	want      any
}) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select("SELECT "+tc.expr+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}))
		})
	}
}
