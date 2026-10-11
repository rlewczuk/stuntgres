package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDomainUniqueRejected(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE domain_s.unique_sentinel(id integer UNIQUE,v integer)", 0))
		run(fixture.Exec("INSERT INTO domain_s.unique_sentinel VALUES(1,7)", 1))
		for _, suffix := range []string{
			"UNIQUE",
			"CONSTRAINT uq UNIQUE",
			"UNIQUE NULLS DISTINCT",
			"UNIQUE NULLS NOT DISTINCT",
			"CONSTRAINT uq UNIQUE NULLS DISTINCT",
			"CONSTRAINT uq UNIQUE NULLS NOT DISTINCT",
		} {
			t.Run(suffix, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					q := "CREATE DOMAIN domain_s.unique_rejected AS integer " + suffix
					want := fixture.ControlsReferenceDiagnostic("42601", "unique constraints not possible for domains")
					want.Position = "51"
					s := fixture.ErrorDiagnostic(q, want)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, conn, recorder, s)
					// Same-name creation succeeds only if rejection published no type.
					// Also preserve the supported explicit domain NULL declaration.
					run(fixture.Exec("CREATE DOMAIN domain_s.unique_rejected AS integer NULL", 0))
					run(fixture.Select("SELECT NULL::domain_s.unique_rejected AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
					run(fixture.Exec("DROP DOMAIN domain_s.unique_rejected", 0))
					sentinel := fixture.Select("SELECT id,v FROM domain_s.unique_sentinel ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}})
					run(sentinel)
					run(fixture.Prepared(sentinel))
				}
			})
		}
	})
}
