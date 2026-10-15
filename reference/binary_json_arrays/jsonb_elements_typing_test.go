package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBElementsTyping(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE DOMAIN jbe_document AS jsonb", 0))
		for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
			for _, call := range []string{strings.ToUpper(f) + "($1)", `"` + f + `"($1)`, "pg_catalog." + f + "($1)", f + "($1::jbe_document)"} {
				jsonTypeBoth(t, p, b, c, jsonbElementsSelect(f, "SELECT * FROM "+call+" AS j", []string{"value"}, [][]any{{"1"}}, `[1]`))
			}
			jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature("SELECT \""+strings.ToUpper(f)+"\"('[]')", strings.ToUpper(f)+"(unknown)", 8))
			q := "SELECT * FROM " + f + "('[1]') AS j(v text)"
			s := jsonTypeError(q, "42601", "a column definition list is only allowed for functions returning \"record\"", strings.Index(q, "v text")+1)
			jsonTypeAnalysisError(t, p, b, c, s)
		}
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE SCHEMA jbe_shadow", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec("SET search_path=jbe_shadow,public", 0))
		for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
			jsonTypeBoth(t, p, b, c, jsonbElementsSelect(f, "SELECT * FROM "+f+"('[1]')", []string{"value"}, [][]any{{"1"}}))
		}
		jsonTypeRun(t, p, b, c, fixture.Exec("RESET search_path", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec("DROP SCHEMA jbe_shadow", 0))
	})
}
