package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCheckConstraintsFeatureSmoke(t *testing.T) {
	iccScenario(t, func(c *iccCase) {
		c.exec("CREATE TABLE icc_smoke_a(v integer CONSTRAINT icc_smoke CHECK(v>0))", "CREATE TABLE icc_smoke_b(v integer CONSTRAINT icc_smoke CHECK(v>0))", "CREATE DOMAIN icc_smoke_d AS integer CONSTRAINT icc_smoke_nn NOT NULL CONSTRAINT icc_smoke_dom CHECK(VALUE>0)")
		a, d, n := c.row("public", "icc_smoke", "(v > 0)"), c.row("public", "icc_smoke_dom", "(VALUE > 0)"), c.row("public", "icc_smoke_nn", "VALUE IS NOT NULL")
		check := c.consumer()
		check(a, a, d, n)
		iccLogicalShape(c)
		c.run(fixture.Select("SELECT count(DISTINCT oid) AS constraints,count(DISTINCT conrelid) AS relations FROM pg_constraint WHERE conname='icc_smoke'", []string{"constraints", "relations"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(2)}}))
		c.exec("CREATE ROLE icc_reader", "GRANT SELECT ON icc_smoke_a,icc_smoke_b TO icc_reader", "GRANT USAGE ON DOMAIN icc_smoke_d TO icc_reader", "SET SESSION AUTHORIZATION icc_reader")
		check()
		c.exec("RESET SESSION AUTHORIZATION")
		check(a, a, d, n)
		c.exec("DROP TABLE icc_smoke_a")
		check(a, d, n)
		c.exec("ALTER TABLE icc_smoke_b ADD CONSTRAINT icc_smoke_pending CHECK(v<100) NOT VALID")
		pending := c.row("public", "icc_smoke_pending", "(v < 100)")
		check(a, d, n, pending)
		c.flags("icc_smoke_pending", false)
		c.exec("ALTER TABLE icc_smoke_b VALIDATE CONSTRAINT icc_smoke_pending")
		check(a, d, n, pending)
		c.flags("icc_smoke_pending", true)
		q := "SELECT * FROM information_schema.check_constraints WHERE constraint_name=$1"
		s := c.step(q, nil, nil)
		c.run(s)
		c.run(fixture.Prepared(s))
		c.hold(q)(s)
		iccNullBoundary(c)
		c.missingColumn()
	})
}
