package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathNavigateQuotedDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			q := `SELECT jsonb_path_query_array($1::jsonb,$2::jsonpath,'{}'::jsonb,$3::boolean) AS changed`
			for _, name := range []struct{ escaped, decoded string }{
				{`a\"b`, `a"b`},
				{`a\\b`, `a\b`},
				{`a\nb`, "a\nb"},
			} {
				for _, silent := range []bool{false, true} {
					variable := `$"` + name.escaped + `"`
					fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "42704", `could not find jsonpath variable "`+name.decoded+`"`, `{}`, variable, silent))
					member := `strict $."` + name.escaped + `"`
					s := jsonpathNavigateError(q, "2203A", `JSON object does not contain key "`+name.decoded+`"`, `{}`, member, silent)
					if silent {
						s = jsonpathNavigateSelect(q, `[]`, `{}`, member, silent)
					}
					fixture.JSONPathNavigateBoth(t, p, b, c, s)
				}
			}
		})
	}
}
