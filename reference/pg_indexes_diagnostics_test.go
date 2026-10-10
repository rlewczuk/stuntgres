package reference_test

import "testing"

func pgIndexesDiagnosticsScenario(t *testing.T, p *referencePair, b referenceBackend) {
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
				s := referenceImplicitError(c.sql, referenceDiagnostic{Code: c.code, Message: c.message, Position: c.position})
				s.selectRows = true
				s.prepared = prepared
				if prepared {
					s.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				pgIndexesCheck(t, p, b, b.db, "pgi_diagnostic_absent", nil)
			})
		}
	}
}
func TestReferencePGIndexesDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE TABLE pgi_diagnostic_table(n INT)", 0))
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE INDEX pgi_diagnostic ON pgi_diagnostic_table(n)", 0))
			pgIndexesDiagnosticsScenario(t, p, b)
			pgIndexesCheck(t, p, b, b.db, "pgi_diagnostic", pgIndexesRow("public", "pgi_diagnostic_table", "pgi_diagnostic", "CREATE INDEX pgi_diagnostic ON public.pgi_diagnostic_table USING btree (n)"))
		})
	}
}
