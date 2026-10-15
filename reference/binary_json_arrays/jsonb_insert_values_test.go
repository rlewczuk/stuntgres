package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbInsertScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func TestReferenceJSONBInsertValues(t *testing.T) {
	jsonbInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ path, before, after string }{
			{"1", `[0, "x", 1, 2]`, `[0, 1, "x", 2]`},
			{"0", `["x", 0, 1, 2]`, `[0, "x", 1, 2]`},
			{"-1", `[0, 1, "x", 2]`, `[0, 1, 2, "x"]`},
			{"-3", `["x", 0, 1, 2]`, `[0, "x", 1, 2]`},
			{"-4", `["x", 0, 1, 2]`, `["x", 0, 1, 2]`},
			{"-2147483648", `["x", 0, 1, 2]`, `["x", 0, 1, 2]`},
			{"3", `[0, 1, 2, "x"]`, `[0, 1, 2, "x"]`},
			{"99", `[0, 1, 2, "x"]`, `[0, 1, 2, "x"]`},
			{"2147483647", `[0, 1, 2, "x"]`, `[0, 1, 2, "x"]`},
		} {
			t.Run(tc.path, func(t *testing.T) {
				base := `SELECT jsonb_insert('[0,1,2]','{` + tc.path + `}','"x"'`
				for _, v := range []struct{ suffix, want string }{{") AS changed", tc.before}, {",false) AS changed", tc.before}, {",true) AS changed", tc.after}} {
					jsonbSetBoth(t, p, b, c, jsonbSetSelect(base+v.suffix, v.want))
				}
			})
		}
		for _, tc := range []struct{ args, want string }{
			{`'{"a":[0,1,2]}','{a,1}','"x"'`, `{"a": [0, "x", 1, 2]}`},
			{`'{"a":[0,1,2]}','{a,1}','"x"',true`, `{"a": [0, 1, "x", 2]}`},
			{`'[]','{0}','"x"'`, `["x"]`}, {`'[]','{-1}','"x"',true`, `["x"]`},
			{`'[]','{99}','"x"'`, `["x"]`}, {`'[]','{-99}','"x"',true`, `["x"]`},
			{`'[0,1]','{1}','[7,8]'`, `[0, [7, 8], 1]`},
			{`'[0,1]','{1}','{"b":2,"a":1}'`, `[0, {"a": 1, "b": 2}, 1]`},
			{`'{"z":0}','{a}','1.00'`, `{"a": 1.00, "z": 0}`},
			{`'{"z":0}','{a}','1.00',true`, `{"a": 1.00, "z": 0}`},
			{`'{"é":1}','{z}','0'`, `{"z": 0, "é": 1}`},
			{`'{}',ARRAY['0'],'1'`, `{"0": 1}`},
			{`'{}',ARRAY[''],'1'`, `{"": 1}`},
			{`'{}',ARRAY[' '],'1'`, `{" ": 1}`},
			{`'{}',ARRAY['a"b'],'1'`, `{"a\"b": 1}`},
			{`'{}','{a,b}','2'`, `{}`},
			{`'{"a":1}','{a,x}','2'`, `{"a": 1}`},
			{`'{"a":null}','{a,x}','2'`, `{"a": null}`},
			{`'[[0]]','{99,0}','2'`, `[[0]]`},
			{`'{}','{}','2'`, `{}`}, {`'[]','{}','2'`, `[]`},
			{`'{"a":1}','{}','2'`, `{"a": 1}`},
			{`'[0,1]','{}','2'`, `[0, 1]`},
			{`'{"a":[0,1,2]}','[-2:-1]={a,1}'::text[],'"x"'`, `{"a": [0, "x", 1, 2]}`},
			{`'[0,1,2]',ARRAY[' +1'],'"x"'`, `[0, "x", 1, 2]`},
			{`'{}',ARRAY['missing',NULL]::text[],'2'`, `{}`},
			{`'{"a":1}',ARRAY['a','x',NULL]::text[],'2'`, `{"a": 1}`},
		} {
			t.Run(tc.args, func(t *testing.T) {
				jsonbSetBoth(t, p, b, c, jsonbSetSelect("SELECT jsonb_insert("+tc.args+") AS changed", tc.want))
			})
		}
	})
}
