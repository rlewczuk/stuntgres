package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonTypeTyping(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, family := range []string{"json", "jsonb"} {
		other := "jsonb"
		if family == "jsonb" {
			other = "json"
		}
		for _, suffix := range []string{"typeof", "array_length", "object_keys"} {
			name := family + "_" + suffix
			for _, tc := range []struct{ args, types string }{
				{"", ""}, {"1", "integer"}, {"'null'::text", "text"},
				{"'null'::" + other, other}, {"NULL,NULL", "unknown, unknown"},
				{"a => NULL", "a => unknown"}, {"a := NULL", "a => unknown"},
			} {
				jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature("SELECT "+name+"("+tc.args+")", name+"("+tc.types+")", 8))
			}
			jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature("/*é*/ SELECT "+name+"(1)", name+"(integer)", 14))
			upper := strings.ToUpper(name)
			jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature("SELECT \""+upper+"\"(NULL)", upper+"(unknown)", 8))
			jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature("SELECT public."+name+"(NULL)", "public."+name+"(unknown)", 8))
			input, typ, value := `null`, "TEXT", any("null")
			if suffix == "array_length" {
				input, typ, value = `[]`, "INT4", int64(0)
			}
			if suffix == "object_keys" {
				input, value = `{"x":1}`, "x"
			}
			for _, spelling := range []string{name, upper, "pg_catalog." + name} {
				jsonTypeBoth(t, p, b, c, fixture.Select("SELECT "+spelling+"("+jsonTypeQuote(input)+") AS v", []string{"v"}, []string{typ}, [][]any{{value}}))
			}
			jsonTypeBoth(t, p, b, c, fixture.Select("SELECT pg_catalog."+name+"(VARIADIC "+jsonTypeQuote(input)+"::"+family+") AS v", []string{"v"}, []string{typ}, [][]any{{value}}))
			jsonTypeBoth(t, p, b, c, fixture.Select("SELECT "+name+"(("+jsonTypeQuote(input)+"::"+other+")::"+family+") AS v", []string{"v"}, []string{typ}, [][]any{{value}}))
			for _, tc := range []struct{ call, decoration, message string }{
				{"DISTINCT " + jsonTypeQuote(input), "DISTINCT", "not an aggregate function"},
				{jsonTypeQuote(input) + " ORDER BY 1", "ORDER BY", "not an aggregate function"},
			} {
				jsonTypeAnalysisError(t, p, b, c, jsonTypeError("SELECT "+name+"("+tc.call+")", "42809", tc.decoration+" specified, but "+name+" is "+tc.message, 8))
			}
			for _, tc := range []struct{ tail, decoration, message string }{
				{" FILTER (WHERE true)", "FILTER", "not an aggregate function"},
				{" OVER ()", "OVER", "not a window function nor an aggregate function"},
			} {
				jsonTypeAnalysisError(t, p, b, c, jsonTypeError("SELECT "+name+"("+jsonTypeQuote(input)+")"+tc.tail, "42809", tc.decoration+" specified, but "+name+" is "+tc.message, 8))
			}
			jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature("SELECT "+name+"(*)", name+"()", 8))
		}
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE DOMAIN jt_"+family+" AS "+family, 0))
		jsonTypeBoth(t, p, b, c, fixture.Select("SELECT "+family+"_typeof('{}'::jt_"+family+") AS k, "+family+"_array_length('[1]'::jt_"+family+") AS n", []string{"k", "n"}, []string{"TEXT", "INT4"}, [][]any{{"object", int64(1)}}))
		jsonTypeBoth(t, p, b, c, fixture.Select("SELECT * FROM "+family+"_object_keys('{\"a\":1}'::jt_"+family+") AS x(k)", []string{"k"}, []string{"TEXT"}, [][]any{{"a"}}))
	}
}

func TestReferenceJSONTypeTyping(t *testing.T) { jsonTypeScenario(t, jsonTypeTyping) }
