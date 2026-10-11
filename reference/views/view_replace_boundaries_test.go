package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceCapabilityBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		run(fixture.Exec("CREATE VIEW v AS SELECT id FROM t", 0))
		for _, query := range []string{"CREATE VIEW excluded WITH(check_option=local) AS SELECT id FROM t", "CREATE OR REPLACE VIEW v WITH(check_option=local) AS SELECT id FROM t", "ALTER VIEW v SET(check_option=local)"} {
			run(fixture.Exec(query, 0))
		}
		for _, query := range []string{"ALTER VIEW v ALTER COLUMN id SET DEFAULT 7", "ALTER VIEW v ALTER COLUMN id DROP DEFAULT"} {
			if backend.PG {
				run(fixture.Exec(query, 0))
			} else {
				run(fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "ALTER VIEW column defaults are unsupported", Position: "14"}))
			}
		}
		run(fixture.Exec("ALTER VIEW v RESET(unknown_option,unknown_option,check_option)", 0))
		run(fixture.Select("SELECT id FROM v", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
}
