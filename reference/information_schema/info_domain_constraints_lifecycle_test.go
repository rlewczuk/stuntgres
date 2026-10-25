package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoDomainConstraintsLifecycle(t *testing.T) {
	dcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		dcSeed(run)
		run(fixture.Exec("CREATE SCHEMA dc_moved", 0))
		where := "domain_schema IN ('dc_scope','dc_moved')"
		held := dcHold(t, p, c, dcProjection(b.PG, where, nil).Query)
		a := dcRow(database, "dc_scope", "same", "a")
		other := dcRow(database, "dc_scope", "same", "b")
		nn := dcRow(database, "dc_scope", "named_nn", "nn")
		upper := dcRow(database, "dc_scope", "upper_bound", "a")
		pending := dcRow(database, "dc_scope", "pending", "a")
		plainNN := dcRow(database, "dc_scope", "plain_not_null", "plain")
		check := func(want ...[]any) {
			dcCheck(t, p, c, r, held, dcProjection(b.PG, where, want))
			run(fixture.Select("SELECT conname FROM pg_catalog.pg_constraint WHERE conrelid='dc_scope.t'::regclass AND contype='c'", []string{"conname"}, []string{"NAME"}, [][]any{{"same"}}))
		}
		check(a, other, nn)
		run(fixture.Exec("ALTER DOMAIN dc_scope.a ADD CONSTRAINT upper_bound CHECK(VALUE<100)", 0))
		check(a, other, nn, upper)
		run(fixture.Exec("ALTER DOMAIN dc_scope.a ADD CONSTRAINT pending CHECK(VALUE<50) NOT VALID", 0))
		check(a, other, nn, upper, pending)
		run(dcFlags("a", "pending", false))
		run(fixture.Exec("ALTER DOMAIN dc_scope.a VALIDATE CONSTRAINT pending", 0))
		check(a, other, nn, upper, pending)
		run(dcFlags("a", "pending", true))
		run(fixture.Exec("ALTER DOMAIN dc_scope.a DROP CONSTRAINT same", 0))
		check(other, nn, upper, pending)
		run(fixture.Exec("ALTER DOMAIN dc_scope.a DROP CONSTRAINT upper_bound", 0))
		check(other, nn, pending)
		run(fixture.Exec("ALTER DOMAIN dc_scope.a DROP CONSTRAINT pending", 0))
		check(other, nn)
		for range 2 {
			run(fixture.Exec("ALTER DOMAIN dc_scope.plain SET NOT NULL", 0))
			check(other, nn, plainNN)
		}
		run(fixture.Exec("ALTER DOMAIN dc_scope.plain DROP NOT NULL", 0))
		check(other, nn)
		run(fixture.Exec("ALTER DOMAIN dc_scope.b RENAME TO renamed", 0))
		check(dcRow(database, "dc_scope", "same", "renamed"), nn)
		run(fixture.Exec("ALTER DOMAIN dc_scope.renamed SET SCHEMA dc_moved", 0))
		check(dcRow(database, "dc_moved", "same", "renamed"), nn)
		run(fixture.Exec("DROP DOMAIN dc_moved.renamed", 0))
		check(nn)
		run(fixture.Exec("DROP DOMAIN dc_scope.nn CASCADE", 0))
		check()
		run(fixture.Exec("DROP DOMAIN dc_scope.a CASCADE", 0))
		check()
		run(fixture.Select("SELECT typname FROM pg_catalog.pg_type WHERE typnamespace='dc_scope'::regnamespace AND typname IN ('child','nn_child','plain')", []string{"typname"}, []string{"NAME"}, [][]any{{"plain"}}))
	})
}
