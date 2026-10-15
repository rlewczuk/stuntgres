package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBElementsValues(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
			jsonTypeBoth(t, p, b, c, jsonbElementsOrdered(f, jsonbElementsMixed, jsonbElementsMixedRows(f)))
			rows := [][]any{{"[]", int64(1)}, {"{}", int64(2)}, {`"é😀"`, int64(3)}, {`"a\\b"`, int64(4)}, {`"a\"b"`, int64(5)}}
			if jsonbElementsType(f) == "TEXT" {
				rows[2][0], rows[3][0], rows[4][0] = "é😀", `a\b`, `a"b`
			}
			jsonTypeBoth(t, p, b, c, jsonbElementsOrdered(f, `[[],{},"é😀","a\\b","a\"b"]`, rows))
			for _, call := range []string{f + "($1)", "pg_catalog." + f + "($1)", f + "(from_json => $1)", f + "(from_json := $1)"} {
				s := jsonbElementsSelect(f, "SELECT "+call, []string{f}, [][]any{{"1"}, {"1"}, {"2"}}, `[1,1,2]`)
				s.Bag = true
				jsonTypeBoth(t, p, b, c, s)
			}
		}
	})
}

func TestReferenceJSONBElementsNulls(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
			for _, input := range []any{`[]`, nil, []byte(nil)} {
				jsonTypeBoth(t, p, b, c, jsonbElementsMetadata(jsonbElementsOrdered(f, input, nil), b.PG))
			}
			for _, literal := range []string{"NULL", "NULL::jsonb", "'[]'::jsonb"} {
				jsonTypeBoth(t, p, b, c, jsonbElementsMetadata(jsonbElementsSelect(f, "SELECT * FROM "+f+"("+literal+")", []string{"value"}, nil), b.PG))
			}
			n, x := any("null"), any(`"null"`)
			if jsonbElementsType(f) == "TEXT" {
				n, x = nil, "null"
			}
			jsonTypeBoth(t, p, b, c, jsonbElementsOrdered(f, `[null,"null"]`, [][]any{{n, int64(1)}, {x, int64(2)}}))
			jsonTypeBoth(t, p, b, c, fixture.Select("SELECT "+f+"('[]') AS a,"+f+"('[null]') AS b", []string{"a", "b"}, []string{jsonbElementsType(f), jsonbElementsType(f)}, [][]any{{nil, n}}))
		}
	})
}

func TestReferenceJSONBElementsMetadata(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
			for _, input := range []any{`[1]`, `[]`, nil, `[null]`} {
				for _, tc := range []struct {
					q       string
					names   []string
					ordinal bool
				}{
					{"SELECT " + f + "($1)", []string{f}, false},
					{"SELECT " + f + "($1) AS result", []string{"result"}, false},
					{"SELECT * FROM " + f + "($1)", []string{"value"}, false},
					{"SELECT * FROM " + f + "($1) AS j", []string{"value"}, false},
					{"SELECT j.value FROM " + f + "($1) AS j", []string{"value"}, false},
					{"SELECT j FROM " + f + "($1) AS j", []string{"j"}, false},
					{"SELECT * FROM " + f + "($1) AS j(v)", []string{"v"}, false},
					{"SELECT * FROM " + f + "($1) WITH ORDINALITY", []string{"value", "ordinality"}, true},
					{"SELECT * FROM " + f + "($1) WITH ORDINALITY AS j(v,n) ORDER BY n", []string{"v", "n"}, true},
				} {
					types := []string{jsonbElementsType(f)}
					var rows [][]any
					if input == `[1]` {
						rows = [][]any{{"1"}}
					}
					if input == `[null]` {
						v := any("null")
						if jsonbElementsType(f) == "TEXT" {
							v = nil
						}
						rows = [][]any{{v}}
					}
					if tc.ordinal {
						types = append(types, "INT8")
						if len(rows) > 0 {
							rows[0] = append(rows[0], int64(1))
						}
					}
					jsonTypeBoth(t, p, b, c, jsonbElementsMetadata(fixture.Select(tc.q, tc.names, types, rows, input), b.PG))
				}
			}
			jsonTypeBoth(t, p, b, c, jsonbElementsMetadata(fixture.Select("SELECT * FROM ROWS FROM ("+f+"('[1]'),"+f+"('[2]'))", []string{"value", "value"}, []string{jsonbElementsType(f), jsonbElementsType(f)}, [][]any{{"1", "2"}}), b.PG))
		}
	})
}
