package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONRecordHeld(t *testing.T) {
	jsonRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, q       string
			set, populate bool
		}{
			{"populate", `SELECT * FROM json_populate_record($1::jr_pair,$2::json)`, false, true},
			{"populate-set", `SELECT * FROM json_populate_recordset($1::jr_pair,$2::json)`, true, true},
			{"to", `SELECT * FROM json_to_record($1::json) AS r(a int,b text)`, false, false},
			{"to-set", `SELECT * FROM json_to_recordset($1::json) AS r(a int,b text)`, true, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.Ctx, tc.q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
				for _, v := range []struct {
					base  any
					input any
					rows  [][]any
					bad   bool
				}{
					{"(7,keep)", `{"a":2}`, [][]any{{int64(2), "keep"}}, false},
					{"(8,new)", []byte(`{"b":null}`), [][]any{{int64(8), nil}}, false},
					{nil, nil, [][]any{{nil, nil}}, false},
					{"(9,last)", `{"a":"bad"}`, nil, true},
					{"(9,last)", `{"b":"recovered"}`, [][]any{{int64(9), "recovered"}}, false},
				} {
					input := v.input
					want := v.rows
					if !tc.populate && !v.bad && input != nil {
						switch stringInput := fixture.Normalize(input); stringInput {
						case `{"a":2}`:
							want = [][]any{{int64(2), nil}}
						case `{"b":null}`:
							want = [][]any{{nil, nil}}
						case `{"b":"recovered"}`:
							want = [][]any{{nil, "recovered"}}
						}
					}
					if tc.set {
						if input != nil {
							text := fixture.Normalize(input).(string)
							if _, bytes := input.([]byte); bytes {
								input = []byte("[" + text + "]")
							} else {
								input = "[" + text + "]"
							}
						} else {
							want = nil
						}
					}
					args := []any{input}
					if tc.populate {
						args = []any{v.base, input}
					}
					if v.bad {
						jsonKeysError(t, p, b, held, tc.q, false, jsonRecordDiagnostic("22P02", `invalid input syntax for type integer: "bad"`, 0), "execute", args...)
						continue
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonRecordPair(tc.q, want, args...))
				}
				if tc.set {
					args := []any{"[]"}
					if tc.populate {
						args = []any{"(7,keep)", "[]"}
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonRecordPair(tc.q, nil, args...))
				}
			})
		}
		for _, prepared := range []bool{false, true} {
			q := `SELECT * FROM json_to_record($1::json) AS r(a int,b text)`
			input := []byte(`{"a":2,"b":"owned"}`)
			var rows *sql.Rows
			var err error
			if prepared {
				statement, e := c.PrepareContext(p.Ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				defer statement.Close()
				rows, err = statement.QueryContext(p.Ctx, input)
			} else {
				rows, err = c.QueryContext(p.Ctx, q, input)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			s := jsonRecordMetadata(jsonRecordPair(q, [][]any{{int64(2), "owned"}}, input), b.PG)
			s.CheckMetadata(t, rows)
			for i := range input {
				input[i] = 'x'
			}
			fixture.StringCoreRetainedRows(t, rows, nil, s.Columns, s.Types, s.Rows, false)
			companion := jsonRecordPair(q, [][]any{{int64(3), "fresh"}}, `{"a":3,"b":"fresh"}`)
			companion.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, companion)
		}
	})
}

func TestReferenceJSONRecordComposition(t *testing.T) {
	jsonRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT * FROM ROWS FROM(json_to_recordset('[{"a":1},{"a":2}]') AS (a int,b text), generate_series(10,12)) WITH ORDINALITY AS r(a,b,g,n) ORDER BY n`, []string{"a", "b", "g", "n"}, []string{"INT4", "TEXT", "INT4", "INT8"}, [][]any{{int64(1), nil, int64(10), int64(1)}, {int64(2), nil, int64(11), int64(2)}, {nil, nil, int64(12), int64(3)}}))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT * FROM ROWS FROM(json_to_recordset('[{"a":1},{"a":2}]') AS (a int), json_to_recordset('[{"b":"x","c":3}]') AS (b text,c int)) WITH ORDINALITY AS r(a,b,c,n) ORDER BY n`, []string{"a", "b", "c", "n"}, []string{"INT4", "TEXT", "INT4", "INT8"}, [][]any{{int64(1), "x", int64(3), int64(1)}, {int64(2), nil, nil, int64(2)}}))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT * FROM ROWS FROM(json_to_recordset('[]') AS (a int,b text), generate_series(1,0)) WITH ORDINALITY AS r(a,b,g,n)`, []string{"a", "b", "g", "n"}, []string{"INT4", "TEXT", "INT4", "INT8"}, nil))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT json_populate_recordset(NULL::jr_pair,'[{"a":1},{"a":2}]')::text AS r,generate_series(10,12) AS g ORDER BY g`, []string{"r", "g"}, []string{"TEXT", "INT4"}, [][]any{{"(1,)", int64(10)}, {"(2,)", int64(11)}, {nil, int64(12)}}))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT * FROM ROWS FROM(json_populate_record(NULL::jr_pair,NULL::json), json_to_recordset('[]') AS (c int)) WITH ORDINALITY AS r(a,b,c,n)`, []string{"a", "b", "c", "n"}, []string{"INT4", "TEXT", "INT4", "INT8"}, [][]any{{nil, nil, nil, int64(1)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE jr_lateral(id int,j json)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jr_lateral VALUES(1,'[{"a":2},{"b":"x"}]'),(2,'[]'),(3,'[{"a":4}]')`, 3))
		for _, lateral := range []string{"", "LATERAL "} {
			jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT s.id,r.a,r.b,r.n FROM jr_lateral s, `+lateral+`ROWS FROM(json_to_recordset(s.j) AS (a int,b text)) WITH ORDINALITY AS r(a,b,n) ORDER BY s.id,r.n`, []string{"id", "a", "b", "n"}, []string{"INT4", "INT4", "TEXT", "INT8"}, [][]any{{int64(1), int64(2), nil, int64(1)}, {int64(1), nil, "x", int64(2)}, {int64(3), int64(4), nil, int64(1)}}))
		}
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT s.id,r.a,r.b,r.n FROM jr_lateral s LEFT JOIN LATERAL ROWS FROM(json_to_recordset(s.j) AS (a int,b text)) WITH ORDINALITY AS r(a,b,n) ON true ORDER BY s.id,r.n`, []string{"id", "a", "b", "n"}, []string{"INT4", "INT4", "TEXT", "INT8"}, [][]any{{int64(1), int64(2), nil, int64(1)}, {int64(1), nil, "x", int64(2)}, {int64(2), nil, nil, nil}, {int64(3), int64(4), nil, int64(1)}}))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT a,n FROM ROWS FROM(json_to_recordset('[{"a":1},{"a":2},{"a":3}]') AS (a int)) WITH ORDINALITY AS r(a,n) WHERE a > 1 ORDER BY n OFFSET 1`, []string{"a", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(3)}}))
	})
}
