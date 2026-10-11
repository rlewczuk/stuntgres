package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func pgIndexesDiagnosticsScenario(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	cases := []struct{ sql, code, message, position string }{
		{"SELECT oid FROM pg_catalog.pg_indexes", "42703", `column "oid" does not exist`, "8"},
		{"SELECT amname FROM pg_catalog.pg_indexes", "42703", `column "amname" does not exist`, "8"},
		{"SELECT /*é*/ missing FROM pg_catalog.pg_indexes", "42703", `column "missing" does not exist`, "14"},
		{"SELECT p.missing FROM pg_catalog.pg_indexes p", "42703", "column p.missing does not exist", "8"},
		{"SELECT * FROM pg_catalog.pg_indexes_missing", "42P01", `relation "pg_catalog.pg_indexes_missing" does not exist`, "15"},
		{"SELECT * FROM missing_schema.pg_indexes", "42P01", `relation "missing_schema.pg_indexes" does not exist`, "15"},
		{`SELECT * FROM pg_catalog."PG_INDEXES"`, "42P01", `relation "pg_catalog.PG_INDEXES" does not exist`, "15"},
		{"SELECT x.indexname FROM pg_catalog.pg_indexes p", "42P01", `missing FROM-clause entry for table "x"`, "8"},
		{"SELECT indexname FROM pg_catalog.pg_indexes a, pg_catalog.pg_indexes b", "42702", `column reference "indexname" is ambiguous`, "8"},
		{"SELECT missing FROM pg_catalog.pg_indexes WHERE FALSE", "42703", `column "missing" does not exist`, "8"},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(c.sql+map[bool]string{false: "/direct", true: "/prepare"}[prepared], func(t *testing.T) {
				s := fixture.ImplicitError(c.sql, fixture.Diagnostic{Code: c.code, Message: c.message, Position: c.position})
				s.SelectRows = true
				s.Prepared = prepared
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.PgIndexesCheck(t, p, b, b.DB, "pgi_diagnostic_absent", nil)
			})
		}
	}
}
func TestReferencePGIndexesDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE pgi_diagnostic_table(n INT)", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE INDEX pgi_diagnostic ON pgi_diagnostic_table(n)", 0))
			pgIndexesDiagnosticsScenario(t, p, b)
			fixture.PgIndexesCheck(t, p, b, b.DB, "pgi_diagnostic", fixture.PgIndexesRow("public", "pgi_diagnostic_table", "pgi_diagnostic", "CREATE INDEX pgi_diagnostic ON public.pgi_diagnostic_table USING btree (n)"))
		})
	}
}
