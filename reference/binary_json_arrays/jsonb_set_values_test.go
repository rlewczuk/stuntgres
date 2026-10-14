package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbSetScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func jsonbSetSelect(q string, want any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"changed"}, []string{"JSONB"}, [][]any{{want}}, args...)
}

func jsonbSetBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	s.Prepared = true
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func TestReferenceJSONBSetValues(t *testing.T) {
	jsonbSetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, want string }{
			{`'{"a":1}','{a}','2'`, `{"a": 2}`},
			{`'{"a":1}','{b}','2'`, `{"a": 1, "b": 2}`},
			{`'{"a":1}','{b}','2',false`, `{"a": 1}`},
			{`'{}','{a,b}','2'`, `{}`},
			{`'{"a":1}','{a,x}','2'`, `{"a": 1}`},
			{`'{"a":null}','{a,x}','2'`, `{"a": null}`},
			{`'{"a":1}','{}','2'`, `{"a": 1}`},
			{`'[]','{}','2'`, `[]`},
			{`'{}','{}','2'`, `{}`},
			{`'[1,2,3]','{-1}','9'`, `[1, 2, 9]`},
			{`'[1,2,3]','{-3}','9'`, `[9, 2, 3]`},
			{`'[1,2,3]','{-4}','9'`, `[9, 1, 2, 3]`},
			{`'[1,2,3]','{3}','9'`, `[1, 2, 3, 9]`},
			{`'[1,2,3]','{3}','9',false`, `[1, 2, 3]`},
			{`'[1,2,3]','{-4}','9',false`, `[1, 2, 3]`},
			{`'[1,2,3]','{2147483647}','9'`, `[1, 2, 3, 9]`},
			{`'[1,2,3]','{-2147483648}','9'`, `[9, 1, 2, 3]`},
			{`'[[1]]','{2147483647,0}','9'`, `[[1]]`},
			{`'[]','{0}','9'`, `[9]`},
			{`'[]','{-1}','9'`, `[9]`},
			{`'[]','{0}','9',false`, `[]`},
			{`'[1,2,3]',ARRAY['+1'],'9'`, `[1, 9, 3]`},
			{`'[1,2,3]',ARRAY[' 1'],'9'`, `[1, 9, 3]`},
			{`'{}',ARRAY['0'],'1.00'`, `{"0": 1.00}`},
			{`'{}',ARRAY[''],'1'`, `{"": 1}`},
			{`'{}',ARRAY[' '],'1'`, `{" ": 1}`},
			{`'{"a":{"b":1}}','[-2:-1]={a,b}'::text[],'2'`, `{"a": {"b": 2}}`},
			{`'{"é":1,"z":0}',ARRAY['é'],'{"x":1.00}'`, `{"z": 0, "é": {"x": 1.00}}`},
			{`'{"a":1}',ARRAY['a','x',NULL]::text[],'2'`, `{"a": 1}`},
			{`'{}',ARRAY['missing',NULL]::text[],'2'`, `{}`},
			{`'[]',ARRAY['bad'],'2',false`, `[]`},
		} {
			t.Run(tc.args, func(t *testing.T) {
				jsonbSetBoth(t, p, b, c, jsonbSetSelect("SELECT jsonb_set("+tc.args+") AS changed", tc.want))
				jsonbSetBoth(t, p, b, c, jsonbSetSelect("SELECT jsonb_set_lax("+tc.args+") AS changed", tc.want))
			})
		}
	})
}
