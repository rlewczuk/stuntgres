package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONExtractFeatureSmoke(t *testing.T) {
	jsonExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonExtractBoth(t, p, b, c, fixture.Select(`SELECT '{"a":1,"a":2}'::json -> 'a' AS a,'[10,20,30]'::json -> -1 AS tail,'{"s":"x\ny"}'::json ->> 's' AS s,'{"n":null}'::json -> 'n' AS raw_null,'{"n":null}'::json ->> 'n' AS text_null,'[]'::json -> 'x' AS miss`, []string{"a", "tail", "s", "raw_null", "text_null", "miss"}, []string{"JSON", "JSON", "TEXT", "JSON", "TEXT", "JSON"}, [][]any{{"2", "30", "x\ny", "null", nil, nil}}))
		jsonExtractPathForms(t, p, b, c, `{"a":[{"b":7}]}`, "{a,0,b}", []string{"a", "0", "b"}, "7", "7")
		jsonExtractPathForms(t, p, b, c, `{"a":7}`, "{}", nil, `{"a":7}`, `{"a":7}`)
		jsonExtractPathForms(t, p, b, c, `{"a":{"b":1},"a":{}}`, "{a,b}", []string{"a", "b"}, "1", "1")
		jsonExtractPathForms(t, p, b, c, `{"x":[0],"a":[10,20,30]}`, "{a,-1}", []string{"a", "-1"}, "10", "10")
		jsonExtractHeld(t, p, b, c)
		jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature("SELECT json_extract_path(1,'a')", "json_extract_path(integer, unknown)", 8))
		jsonTypeBoth(t, p, b, c, jsonExtractUnicodeError("SELECT $1::json ->> 'x' AS v", `{"x":"\u0000"}`, `{"x":"\u0000"}`))
		jsonExtractBoth(t, p, b, c, jsonExtractOne(`SELECT '{"x":"ok"}'::json ->> 'x' AS v`, "TEXT", "ok"))
		check := jsonExtractStored(t, p, b, c)
		jsonExtractControls(t, p, b, c, check)
	})
}
