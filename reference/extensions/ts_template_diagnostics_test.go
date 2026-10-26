package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func tsTemplateDiagnostic(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, query, position string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: position})
		s.SelectRows = true
		s.Prepared = prepared
		s.ErrorStage = "execute"
		if prepared { s.ErrorStage = "prepare" }
		fixture.RunStep(t, p.Ctx, c, r, s)
		recovery := fixture.Select(tsTemplateQuery, tsTemplateNames, tsTemplateTypes, nil, "ts_template_absent")
		recovery.Prepared = prepared
		recovery.CheckMetadata = func(t *testing.T, rows *sql.Rows) { tsTemplateMetadata(t, rows, pg) }
		fixture.RunStep(t, p.Ctx, c, r, recovery)
	}
}

func TestReferenceTSTemplateDiagnostics(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, installed := range []bool{false, true} {
			if installed {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SCHEMA ext_addon", 0))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
			}
			before := tsTemplateRead(t, p, c, pg, installed)
			tsTemplateDiagnostic(t, p, c, r, pg, "SELECT missing FROM pg_catalog.pg_ts_template WHERE FALSE", "8")
			tsTemplateDiagnostic(t, p, c, r, pg, "SELECT /*é*/ missing FROM pg_catalog.pg_ts_template WHERE FALSE", "14")
			if tsTemplateRead(t, p, c, pg, installed) != before { t.Fatal("diagnostic changed installed template") }
			if installed { tsTemplateLinked(t, p, c, r, pg) }
		}
	})
}
