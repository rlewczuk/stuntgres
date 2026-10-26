package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func tsDictDiagnostic(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, query, position string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: position})
		s.SelectRows = true
		s.Prepared = prepared
		s.ErrorStage = "execute"
		if prepared { s.ErrorStage = "prepare" }
		fixture.RunStep(t, p.Ctx, c, r, s)
	}
}

func TestReferenceTSDictDiagnostics(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, installed := range []bool{false, true} {
			if installed {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SCHEMA ext_addon", 0))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
			}
			tsDictDiagnostic(t, p, c, r, "SELECT missing FROM pg_catalog.pg_ts_dict WHERE FALSE", "8")
			tsDictDiagnostic(t, p, c, r, "SELECT /*é*/ missing FROM pg_catalog.pg_ts_dict WHERE FALSE", "14")
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(tsDictQuery, tsDictNames, tsDictTypes, nil, "ts_dict_absent")
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { tsDictMetadata(t, rows, pg) }
				fixture.RunStep(t, p.Ctx, c, r, s)
			}
		}
	})
}
