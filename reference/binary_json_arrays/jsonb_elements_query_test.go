package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBElementsParameters(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
			for _, from := range []bool{false, true} {
				q, col := "SELECT "+f+"($1)", f
				if from {
					q, col = "SELECT * FROM "+f+"($1) AS j", "value"
				}
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				n, x := any("null"), any(`"x"`)
				if jsonbElementsType(f) == "TEXT" {
					n, x = nil, "x"
				}
				for _, tc := range []struct {
					input   any
					rows    [][]any
					message string
				}{
					{`[1,null,"x"]`, [][]any{{"1"}, {n}, {x}}, ""}, {`[]`, nil, ""}, {nil, nil, ""}, {`{}`, nil, "cannot extract elements from an object"}, {`true`, nil, "cannot extract elements from a scalar"}, {`[2]`, [][]any{{"2"}}, ""}, {[]byte(nil), nil, ""}, {`[3]`, [][]any{{"3"}}, ""},
				} {
					s := jsonbElementsSelect(f, q, []string{col}, tc.rows, tc.input)
					s.Bag = true
					if tc.message != "" {
						s = jsonTypeError(q, "22023", tc.message, 0, tc.input)
					}
					jsonTypeRun(t, p, b, held, s)
				}
				d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
				d.Detail = `Expected JSON value, but found "}".`
				d.Context = "JSON data, line 1: {\"a\":}\nunnamed portal parameter $1 = '...'"
				s := fixture.ErrorDiagnostic(q, d, `{"a":}`)
				s.SelectRows = true
				s.ErrorStage = "execute"
				jsonTypeRun(t, p, b, held, s)
				jsonTypeRun(t, p, b, held, jsonbElementsSelect(f, q, []string{col}, [][]any{{"4"}}, `[4]`))
				for _, tc := range []struct {
					input string
					n     int64
				}{{`[1,2,3]`, 3}, {`[]`, 0}, {`[1]`, 1}} {
					jsonTypeRun(t, p, b, held, fixture.Exec(q, tc.n, tc.input))
				}
				input := []byte(`[7]`)
				rows, err := stmt.QueryContext(p.Ctx, input)
				if err != nil {
					t.Fatal(err)
				}
				input[1] = '9'
				fixture.StringCoreRetainedRows(t, rows, nil, []string{col}, []string{jsonbElementsType(f)}, [][]any{{"7"}}, b.PG)
				rows.Close()
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}

func TestReferenceJSONBElementsQuery(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbe_query(id int,j jsonb)", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbe_query VALUES(1,'[10,null,20]'),(2,'[]'),(3,NULL)`, 3))
		for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
			n := any("null")
			if jsonbElementsType(f) == "TEXT" {
				n = nil
			}
			for _, lateral := range []string{"", "LATERAL "} {
				jsonTypeBoth(t, p, b, c, fixture.Select("SELECT s.id,e.v,e.n FROM jbe_query s,"+lateral+f+"(s.j) WITH ORDINALITY AS e(v,n) ORDER BY s.id,e.n", []string{"id", "v", "n"}, []string{"INT4", jsonbElementsType(f), "INT8"}, [][]any{{int64(1), "10", int64(1)}, {int64(1), n, int64(2)}, {int64(1), "20", int64(3)}}))
			}
			jsonTypeBoth(t, p, b, c, fixture.Select("SELECT s.id,e.v,e.n FROM jbe_query s LEFT JOIN LATERAL "+f+"(s.j) WITH ORDINALITY AS e(v,n) ON true ORDER BY s.id,e.n", []string{"id", "v", "n"}, []string{"INT4", jsonbElementsType(f), "INT8"}, [][]any{{int64(1), "10", int64(1)}, {int64(1), n, int64(2)}, {int64(1), "20", int64(3)}, {int64(2), nil, nil}, {int64(3), nil, nil}}))
			s := fixture.Select("SELECT "+f+"('[1,2]') AS a,"+f+"('[3]') AS b", []string{"a", "b"}, []string{jsonbElementsType(f), jsonbElementsType(f)}, [][]any{{"1", "3"}, {"2", nil}})
			s.Bag = true
			jsonTypeBoth(t, p, b, c, s)
			jsonTypeBoth(t, p, b, c, fixture.Select("SELECT * FROM ROWS FROM ("+f+"('[1,2]'),"+f+"('[3]')) WITH ORDINALITY AS e(a,b,n) ORDER BY n", []string{"a", "b", "n"}, []string{jsonbElementsType(f), jsonbElementsType(f), "INT8"}, [][]any{{"1", "3", int64(1)}, {"2", nil, int64(2)}}))
			s = jsonbElementsSelect(f, "SELECT "+f+"(jsonb_array_elements('[[1],[2]]')) AS v", []string{"v"}, [][]any{{"1"}, {"2"}})
			s.Bag = true
			jsonTypeBoth(t, p, b, c, s)
			jsonTypeBoth(t, p, b, c, fixture.Select("SELECT DISTINCT e.value::text AS v FROM "+f+"('[2,1,2]') e ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"1"}, {"2"}}))
			jsonTypeBoth(t, p, b, c, fixture.Select("SELECT e.value::text AS v,count(*) AS n FROM "+f+"('[2,1,2]') e GROUP BY e.value::text ORDER BY v", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"1", int64(1)}, {"2", int64(2)}}))
			jsonTypeBoth(t, p, b, c, fixture.Select("SELECT length(e.value::text) AS n FROM "+f+"('[12]') e", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}))
		}
	})
}
