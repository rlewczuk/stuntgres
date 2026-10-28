package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonpathNavigateScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func jsonpathNavigateSelect(q string, want any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"changed"}, []string{"JSONB"}, [][]any{{want}}, args...)
}

func jsonpathNavigateError(q, code, message string, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(code, message), args...)
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}

type jsonpathNavigateCase struct{ doc, path, vars, want string }

func jsonpathNavigateCases(t *testing.T, cases []jsonpathNavigateCase) {
	t.Helper()
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range cases {
			t.Run(tc.path+"/"+tc.doc, func(t *testing.T) {
				vars := tc.vars
				if vars == "" {
					vars = "{}"
				}
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect("SELECT jsonb_path_query_array($1::jsonb,$2::jsonpath,$3::jsonb) AS changed", tc.want, tc.doc, tc.path, vars))
			})
		}
	})
}
