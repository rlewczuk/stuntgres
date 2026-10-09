package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDomainUniqueRejected(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("CREATE TABLE domain_s.unique_sentinel(id integer UNIQUE,v integer)", 0))
		run(referenceExec("INSERT INTO domain_s.unique_sentinel VALUES(1,7)", 1))
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
					want := controlsReferenceDiagnostic("42601", "unique constraints not possible for domains")
					want.Position = "51"
					s := referenceErrorDiagnostic(q, want)
					s.prepared = prepared
					s.errorStage = "execute"
					referenceRunStep(t, p.ctx, conn, recorder, s)
					// Same-name creation succeeds only if rejection published no type.
					// Also preserve the supported explicit domain NULL declaration.
					run(referenceExec("CREATE DOMAIN domain_s.unique_rejected AS integer NULL", 0))
					run(referenceSelect("SELECT NULL::domain_s.unique_rejected AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
					run(referenceExec("DROP DOMAIN domain_s.unique_rejected", 0))
					sentinel := referenceSelect("SELECT id,v FROM domain_s.unique_sentinel ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}})
					run(sentinel)
					run(referencePrepared(sentinel))
				}
			})
		}
	})
}
