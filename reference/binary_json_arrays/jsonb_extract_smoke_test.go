package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBExtractFeatureSmoke(t *testing.T) {
	jsonbExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonExtractBoth(t, p, b, c, fixture.Select(`SELECT '{"a":1,"a":2}'::jsonb->'a' AS dup,'[10,20,30]'::jsonb->-1 AS tail,'{"a":"x\ny"}'::jsonb->>'a' AS decoded,'{"a":null}'::jsonb->'a' AS raw_null,'{"a":null}'::jsonb->>'a' AS text_null,'{}'::jsonb->'missing' AS missing,'[]'::jsonb->'0' AS wrong_shape`, []string{"dup", "tail", "decoded", "raw_null", "text_null", "missing", "wrong_shape"}, []string{"JSONB", "JSONB", "TEXT", "JSONB", "TEXT", "JSONB", "JSONB"}, [][]any{{"2", "30", "x\ny", "null", nil, nil, nil}}))
		jsonbExtractPathForms(t, p, b, c, `{"a":[{"b":7}]}`, "{a,0,b}", []string{"a", "0", "b"}, "7", "7")
		jsonbExtractPathForms(t, p, b, c, `{"z":1,"a":2}`, "{}", nil, `{"a": 2, "z": 1}`, `{"a": 2, "z": 1}`)
		jsonbExtractPathForms(t, p, b, c, `{"a":[{"b":{"c":9}}]}`, "{{a,0},{b,c}}", nil, "9", "9")
		jsonbExtractPathForms(t, p, b, c, `{"a":[{"b":7}]}`, "[0:2]={a,0,b}", nil, "7", "7")
		jsonExtractBoth(t, p, b, c, fixture.Select(`SELECT '7'::jsonb->0 AS zero,'7'::jsonb->-1 AS last,'7'::jsonb#>'{0}' AS path,'{"a":{"b":1},"a":{}}'::jsonb#>'{a,b}' AS duplicate_path,'{"x":[0],"a":[10,20,30]}'::jsonb#>'{a,-1}' AS sibling_tail`, []string{"zero", "last", "path", "duplicate_path", "sibling_tail"}, []string{"JSONB", "JSONB", "JSONB", "JSONB", "JSONB"}, [][]any{{"7", "7", nil, nil, "30"}}))
		jsonbExtractHeld(t, p, b, c)
		q := `/*é*/ SELECT '{}'::jsonb -> 1::bigint`
		s := jsonTypeError(q, "42883", "operator does not exist: jsonb -> bigint", len([]rune(q[:strings.Index(q, "->")]))+1)
		s.Diagnostic.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		jsonTypeAnalysisError(t, p, b, c, s)
		jsonExtractBoth(t, p, b, c, jsonExtractOne(`SELECT '{"a":"ok"}'::jsonb ->> 'a' AS v`, "TEXT", "ok"))
		check := jsonbExtractStored(t, p, b, c)
		jsonbExtractControls(t, p, b, c, check)
	})
}
