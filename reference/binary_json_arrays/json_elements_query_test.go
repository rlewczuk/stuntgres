package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonElementsQuery(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE jelements_source(id int,j json)", 0))
	run(fixture.Prepared(fixture.Exec("INSERT INTO jelements_source VALUES($1,$2),($3,$4),($5,$6)", 3, 1, `[1,null]`, 2, `[]`, 3, nil)))
	for _, f := range []string{"json_array_elements", "json_array_elements_text"} {
		n := any("null")
		if f == "json_array_elements_text" {
			n = nil
		}
		for _, lateral := range []string{"", "LATERAL "} {
			fixture.JSONKeysBoth(t, p, b, c, fixture.Select("SELECT s.id,e.value,e.n FROM jelements_source s,"+lateral+f+"(s.j) WITH ORDINALITY AS e(value,n) ORDER BY s.id,e.n", []string{"id", "value", "n"}, []string{"INT4", jsonElementsType(f), "INT8"}, [][]any{{int64(1), "1", int64(1)}, {int64(1), n, int64(2)}}))
		}
		fixture.JSONKeysBoth(t, p, b, c, fixture.Select("SELECT s.id,e.value,e.n FROM jelements_source s LEFT JOIN LATERAL "+f+"(s.j) WITH ORDINALITY AS e(value,n) ON true ORDER BY s.id,e.n", []string{"id", "value", "n"}, []string{"INT4", jsonElementsType(f), "INT8"}, [][]any{{int64(1), "1", int64(1)}, {int64(1), n, int64(2)}, {int64(2), nil, nil}, {int64(3), nil, nil}}))
		s := fixture.Select("SELECT "+f+"('[1,2]'::json) AS v,generate_series(7,9) AS g", []string{"v", "g"}, []string{jsonElementsType(f), "INT4"}, [][]any{{"1", int64(7)}, {"2", int64(8)}, {nil, int64(9)}})
		s.Bag = true
		fixture.JSONKeysBoth(t, p, b, c, s)
		fixture.JSONKeysBoth(t, p, b, c, fixture.Select("SELECT * FROM ROWS FROM("+f+"('[1,2]'::json),generate_series(7,9)) WITH ORDINALITY AS z(v,g,n) ORDER BY n", []string{"v", "g", "n"}, []string{jsonElementsType(f), "INT4", "INT8"}, [][]any{{"1", int64(7), int64(1)}, {"2", int64(8), int64(2)}, {nil, int64(9), int64(3)}}))
		s = fixture.Select("SELECT a.value AS a,b.value AS b FROM "+f+"('[1,2]') a,"+f+"('[3,4]') b", []string{"a", "b"}, []string{jsonElementsType(f), jsonElementsType(f)}, [][]any{{"1", "3"}, {"1", "4"}, {"2", "3"}, {"2", "4"}})
		s.Bag = true
		fixture.JSONKeysBoth(t, p, b, c, s)
	}
	for _, q := range []string{"SELECT json_array_elements(json_array_elements('[[1,2],[],[3]]'::json)) AS v", "SELECT json_array_elements(json_array_elements($1)) AS v"} {
		args := []any(nil)
		if q == "SELECT json_array_elements(json_array_elements($1)) AS v" {
			args = []any{`[[1,2],[],[3]]`}
		}
		s := fixture.Select(q, []string{"v"}, []string{"JSON"}, [][]any{{"1"}, {"2"}, {"3"}}, args...)
		s.Bag = true
		fixture.JSONKeysBoth(t, p, b, c, s)
	}
	fixture.JSONKeysBoth(t, p, b, c, fixture.Select("SELECT * FROM ROWS FROM(json_array_elements_text('[null]'),generate_series(7,8)) WITH ORDINALITY AS z(v,g,n) ORDER BY n", []string{"v", "g", "n"}, []string{"TEXT", "INT4", "INT8"}, [][]any{{nil, int64(7), int64(1)}, {nil, int64(8), int64(2)}}))
	fixture.JSONKeysBoth(t, p, b, c, fixture.Select("SELECT value,count(*) AS n FROM json_array_elements_text('[\"b\",\"a\",\"b\"]') GROUP BY value ORDER BY value", []string{"value", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {"b", int64(2)}}))
}

func TestReferenceJSONElementsQueryStructure(t *testing.T) {
	jsonElementsScenario(t, jsonElementsQuery)
}
