package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoDomainConstraintsPopulation(t *testing.T) {
	dcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		dcSeed(run)
		s := dcProjection(b.PG, "domain_schema='dc_scope'", dcSeedRows(database))
		dcCheck(t, p, c, r, dcHold(t, p, c, s.Query), s)
		bootstrap := dcProjection(b.PG, "domain_schema='information_schema' AND domain_name IN ('cardinal_number','yes_or_no')", [][]any{
			dcRow(database, "information_schema", "cardinal_number_domain_check", "cardinal_number"),
			dcRow(database, "information_schema", "yes_or_no_check", "yes_or_no"),
		})
		dcCheck(t, p, c, r, dcHold(t, p, c, bootstrap.Query), bootstrap)
	})
}
