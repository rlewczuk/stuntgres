package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoDomainUdtUsageRows(t *testing.T) {
	duScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		duSeed(run)
		want := duSeedRows(database)
		s := duProjection(b.PG, "domain_schema='du_scope'", want)
		held := cduHold(t, p, c, s.Query)
		check := func() { duCheck(t, p, c, r, held, s) }
		check()
		// An array whose element is a domain, and unused enum/composite types,
		// are not domains. Column/routine references never multiply this bag.
		for _, q := range []string{
			"ALTER DOMAIN du_scope.p SET DEFAULT 7",
			"ALTER DOMAIN du_scope.p ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID",
			"ALTER DOMAIN du_scope.p VALIDATE CONSTRAINT positive",
			"ALTER DOMAIN du_scope.p SET NOT NULL",
			"ALTER DOMAIN du_scope.p DROP NOT NULL",
			"ALTER DOMAIN du_scope.p DROP CONSTRAINT positive",
			"ALTER DOMAIN du_scope.p DROP DEFAULT",
			"CREATE TABLE du_scope.uses(v du_scope.c, a du_scope.p[])",
			"CREATE FUNCTION du_scope.identity(v du_scope.c) RETURNS du_scope.c LANGUAGE SQL RETURN v",
			"CREATE TYPE du_scope.unused_enum AS ENUM('a','b')",
			"CREATE TYPE du_scope.unused_composite AS(x text)",
			"DROP FUNCTION du_scope.identity(du_scope.c)",
			"DROP TABLE du_scope.uses",
		} {
			run(fixture.Exec(q, 0))
			check()
		}
		for _, q := range []string{"CREATE SCHEMA \"Du Quoted\"", "CREATE TYPE \"Du Quoted\".\"Enum Base\" AS ENUM('x')", "CREATE DOMAIN \"Du Quoted\".\"Mixed Domain\" AS \"Du Quoted\".\"Enum Base\""} {
			run(fixture.Exec(q, 0))
		}
		quoted := duProjection(b.PG, "domain_schema='Du Quoted'", [][]any{duRow(database, "Du Quoted", "Enum Base", "Du Quoted", "Mixed Domain")})
		duCheck(t, p, c, r, cduHold(t, p, c, quoted.Query), quoted)
		check()
	})
}

func TestReferenceInfoDomainUdtUsageBuiltinDomains(t *testing.T) {
	duScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		s := duProjection(b.PG, duBuiltinWhere, duBuiltinRows(database))
		held := cduHold(t, p, c, s.Query)
		duCheck(t, p, c, r, held, s)
		run(fixture.Exec("CREATE ROLE du_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION du_reader", 0))
		duCheck(t, p, c, r, held, duProjection(b.PG, duBuiltinWhere, nil))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		duCheck(t, p, c, r, held, s)
	})
}
