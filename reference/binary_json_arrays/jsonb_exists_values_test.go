package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBExistsValues(t *testing.T) {
	jsonbExistsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cases := []struct {
			expr string
			want any
		}{
			{`'{"a":null,"b":"value"}'::jsonb ? 'a'`, true},
			{`'{"a":null,"b":"value"}'::jsonb ? 'value'`, false},
			{`'["a",1,true,null,["nested"],{"k":"v"}]'::jsonb ? 'a'`, true},
			{`'"a"'::jsonb ? 'a'`, true},
			{`'1'::jsonb ? '1'`, false}, {`'true'::jsonb ? 'true'`, false}, {`'null'::jsonb ? 'null'`, false},
			{`'{"outer":{"a":1}}'::jsonb ? 'a'`, false},
			{`'{"a":1}'::jsonb ?| ARRAY['missing','a']`, true},
			{`'{"a":1}'::jsonb ?& ARRAY['a','missing']`, false},
			{`'{"a":1}'::jsonb ?& ARRAY['a','a',NULL]`, true},
			{`'{}'::jsonb ?| '{}'::text[]`, false}, {`'{}'::jsonb ?& '{}'::text[]`, true},
			{`'{}'::jsonb ?| ARRAY[NULL]::text[]`, false}, {`'{}'::jsonb ?& ARRAY[NULL]::text[]`, true},
			{`NULL::jsonb ? 'a'`, nil}, {`'{}'::jsonb ? NULL::text`, nil},
			{`'{}'::jsonb ?| NULL::text[]`, nil}, {`'{}'::jsonb ?& NULL::text[]`, nil},
			{`NULL::jsonb ?& '{}'::text[]`, nil}, {`NULL::jsonb ?| '{}'::text[]`, nil},
			{`'null'::jsonb ?& '{}'::text[]`, true}, {`'null'::jsonb ?| '{}'::text[]`, false},
			{`'{"a":1}'::jsonb ?| '[-1:0][3:4]={{a,NULL},{a,b}}'::text[]`, true},
			{`'{"a":1}'::jsonb ?& '[-1:0][3:4]={{a,NULL},{a,b}}'::text[]`, false},
			{`'{"a":1}'::jsonb ?| '[-1:0][3:4]={{a,NULL},{a,a}}'::text[]`, true},
			{`'{"a":1}'::jsonb ?& '[-1:0][3:4]={{a,NULL},{a,a}}'::text[]`, true},
			{`'{}'::jsonb ?| '{{},{}}'::text[]`, false}, {`'{}'::jsonb ?& '{{},{}}'::text[]`, true},
			{`'{"":0,"NULL":1,"\u0061":2,"é":3,"a":4}'::jsonb ? ''`, true},
			{`'{"\u0061":1}'::jsonb ? 'a'`, true},
			{`'{"a":1,"a":null}'::jsonb ? 'a'`, true},
			{`'{"a":1}'::jsonb ? 'A'`, false},
			{`'{"é":1}'::jsonb ? 'é'`, true}, {`'{"é":1}'::jsonb ? 'é'`, false},
			{`'[""]'::jsonb ? ''`, true}, {`'""'::jsonb ? ''`, true},
			{`'["NULL"]'::jsonb ?& ARRAY['NULL',NULL]`, true},
			{`jsonb_object('{a,1}'::text[]) ? 'a'`, true},
			{`'{"a":1}'::jsonb ? lower('A')`, true},
			{`'{"a":1}'::jsonb OPERATOR(pg_catalog.?) 'a'`, true},
			{`'{"a":1}'::jsonb OPERATOR(pg_catalog.?|) '{a}'`, true},
			{`'{"a":1}'::jsonb OPERATOR(pg_catalog.?&) '{a}'`, true},
		}
		for _, key := range []string{"1", "true", "null", "nested", "k"} {
			cases = append(cases, struct {
				expr string
				want any
			}{`'["a",1,true,null,["nested"],{"k":"v"}]'::jsonb ? '` + key + `'`, false})
		}
		for _, tc := range cases {
			t.Run(tc.expr, func(t *testing.T) { jsonbExistsBoth(t, p, b, c, jsonbExistsSelect("SELECT "+tc.expr+" AS v", tc.want)) })
		}
	})
}
