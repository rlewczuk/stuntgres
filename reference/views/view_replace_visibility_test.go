package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceDependencyVisibility(t *testing.T) {
	for _, tc := range []struct{ name, view, dependent, path, shadow, description, detail string }{
		{"moved", "v", "w", "public", "", "s.v", "w"},
		{"visible", "v", "w", "s,public", "", "v", "w"},
		{"shadowed", "v", "w", "public,s", "CREATE VIEW v AS SELECT id FROM t", "s.v", "w"},
		{"quoted", `"odd view"`, `"odd dependent"`, "public", "", `s."odd view"`, `"odd dependent"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			fixture.SchemaReferenceCleanup(t, "s")
			for _, q := range []string{"CREATE SCHEMA s", "CREATE TABLE t(id INT)", "CREATE VIEW s." + tc.view + " AS SELECT id FROM t", "CREATE VIEW " + tc.dependent + " AS SELECT id FROM s." + tc.view} {
				p.Step(t, fixture.Exec(q, 0))
			}
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(7)", 1))
			if tc.shadow != "" {
				p.Step(t, fixture.Exec(tc.shadow, 0))
			}
			p.Step(t, fixture.Exec("SET search_path TO "+tc.path, 0))
			step := fixture.ImplicitError("DROP VIEW s."+tc.view, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop view " + tc.description + " because other objects depend on it", Detail: "view " + tc.detail + " depends on view " + tc.description, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})

			t.Run("paired", func(t *testing.T) { p.Step(t, step) })
			p.Step(t, fixture.Select("SELECT id FROM "+tc.dependent, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
