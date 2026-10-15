package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonExtractValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	doc := `{"a":1,"a":2,"0":"zero","s":"x\ny","n":null,"v":{ "z":1, "z":2 },"num":1e+09,"big":123456789012345678901234567890,"bool":true}`
	for _, tc := range []struct {
		key       string
		raw, text any
	}{
		{"a", "2", "2"}, {"0", `"zero"`, "zero"}, {"s", `"x\ny"`, "x\ny"}, {"n", "null", nil},
		{"v", `{ "z":1, "z":2 }`, `{ "z":1, "z":2 }`}, {"num", "1e+09", "1e+09"},
		{"big", "123456789012345678901234567890", "123456789012345678901234567890"}, {"bool", "true", "true"}, {"missing", nil, nil}, {"A", nil, nil},
	} {
		t.Run(tc.key, func(t *testing.T) {
			jsonExtractBoth(t, p, b, c, jsonExtractOne("SELECT $1::json -> $2::text AS v", "JSON", tc.raw, doc, tc.key))
			jsonExtractBoth(t, p, b, c, jsonExtractOne("SELECT $1::json ->> $2::text AS v", "TEXT", tc.text, doc, tc.key))
		})
	}
	for _, tc := range []struct {
		index int
		want  any
	}{{0, "10"}, {1, "20"}, {-1, "30"}, {-3, "10"}, {-4, nil}, {3, nil}, {-2147483648, nil}, {2147483647, nil}} {
		for _, op := range []string{"->", "->>"} {
			typ := "JSON"
			if op == "->>" {
				typ = "TEXT"
			}
			jsonExtractBoth(t, p, b, c, jsonExtractOne("SELECT $1::json "+op+" $2::int AS v", typ, tc.want, "[10,20,30]", tc.index))
		}
	}
	for _, doc := range []any{"{}", "[]", "0", "true", "null", `"s"`, nil} {
		for _, tc := range []struct {
			op, typ, cast string
			arg           any
		}{{"->", "JSON", "text", "missing"}, {"->>", "TEXT", "text", "0"}, {"->", "JSON", "int", 0}, {"->>", "TEXT", "int", -1}} {
			jsonExtractBoth(t, p, b, c, jsonExtractOne("SELECT $1::json "+tc.op+" $2::"+tc.cast+" AS v", tc.typ, nil, doc, tc.arg))
		}
	}
	for _, tc := range []struct{ op, typ, cast string }{{"->", "JSON", "text"}, {"->>", "TEXT", "int"}} {
		jsonExtractBoth(t, p, b, c, jsonExtractOne("SELECT $1::json "+tc.op+" $2::"+tc.cast+" AS v", tc.typ, nil, doc, nil))
	}
	jsonExtractBoth(t, p, b, c, jsonExtractOne(`SELECT '{"\u0061":1,"a":2}'::json -> 'a' AS v`, "JSON", "2"))
}
func TestReferenceJSONExtractValues(t *testing.T) { jsonExtractScenario(t, jsonExtractValues) }
