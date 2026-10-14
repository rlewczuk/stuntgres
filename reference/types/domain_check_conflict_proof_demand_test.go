package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckConflictProofDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) {
					t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
					if t.Failed() {
						t.FailNow()
					}
				}
				run(fixture.Exec("CREATE DOMAIN domain_s.proof_positive AS integer CONSTRAINT proof_positive_check CHECK(VALUE>0)", 0))
				run(fixture.Exec("CREATE TABLE domain_s.proof_target(id integer)", 0))
				run(fixture.Exec("CREATE UNIQUE INDEX proof_domain_index ON domain_s.proof_target(id) WHERE 0::domain_s.proof_positive>0", 0))
				s := fixture.Exec("INSERT INTO domain_s.proof_target(id) SELECT v FROM public.one WHERE false ON CONFLICT(id) WHERE 0::domain_s.proof_positive>0 DO NOTHING", 0)
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT id FROM domain_s.proof_target", []string{"id"}, []string{"INT4"}, nil))
				// Structural arbiter proof is not a row consuming the predicate.
				// A real inserted row does consume it and must still fail CHECK.
				run(domainCheckFailure("INSERT INTO domain_s.proof_target VALUES(1)", "proof_positive", "proof_positive_check", false, prepared))
				run(fixture.Select("SELECT id FROM domain_s.proof_target", []string{"id"}, []string{"INT4"}, nil))
			})
		})
	}
}
