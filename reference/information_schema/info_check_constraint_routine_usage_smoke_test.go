package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCheckConstraintRoutineUsageFeatureSmoke(t *testing.T) {
	ccruScenario(t, func(c *ccruCase) {
		f, g := c.seed()
		c.exec("CREATE ROLE ccru_o", "CREATE ROLE ccru_r", "CREATE TABLE ccru_smoke_a(v integer CONSTRAINT ccru_smoke CHECK(ccru_f(v)))", "CREATE TABLE ccru_smoke_b(v integer CONSTRAINT ccru_smoke CHECK(ccru_g(v)))", "CREATE TABLE ccru_smoke_c(v integer CONSTRAINT ccru_smoke CHECK(ccru_f(v)))", "ALTER FUNCTION ccru_f(integer) OWNER TO ccru_o", "ALTER FUNCTION ccru_g(integer) OWNER TO ccru_o")
		want := [][]any{c.row("public", "ccru_smoke", "public", f), c.row("public", "ccru_smoke", "public", g)}
		c.read(want...)
		held := c.hold(ccruQuery)
		q := `SELECT c.conname,r.relname,c.contype,c.convalidated,c.conenforced FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid WHERE c.conname='ccru_smoke' ORDER BY r.relname`
		c.run(fixture.Select(q, []string{"conname", "relname", "contype", "convalidated", "conenforced"}, []string{"NAME", "NAME", "CHAR", "BOOL", "BOOL"}, [][]any{{"ccru_smoke", "ccru_smoke_a", "c", true, true}, {"ccru_smoke", "ccru_smoke_b", "c", true, true}, {"ccru_smoke", "ccru_smoke_c", "c", true, true}}))
		c.run(fixture.Select("SELECT count(DISTINCT oid) AS identities,count(DISTINCT conrelid) AS tables FROM pg_constraint WHERE conname='ccru_smoke'", []string{"identities", "tables"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}}))
		c.exec("GRANT USAGE ON SCHEMA public TO ccru_r", "GRANT SELECT ON ccru_smoke_a,ccru_smoke_b,ccru_smoke_c TO ccru_r", "GRANT EXECUTE ON FUNCTION ccru_f(integer),ccru_g(integer) TO ccru_r", "SET ROLE ccru_r")
		c.read()
		held(c.step(ccruQuery, nil))
		c.exec("RESET ROLE", "SET ROLE ccru_o")
		c.read(want...)
		held(c.step(ccruQuery, want))
		c.exec("RESET ROLE", "DROP TABLE ccru_smoke_a")
		c.read(want...)
		held(c.step(ccruQuery, want))
		c.exec("DROP TABLE ccru_smoke_c")
		c.read(want[1])
		held(c.step(ccruQuery, want[1:]))
		q = "SELECT * FROM information_schema.check_constraint_routine_usage WHERE constraint_name=$1"
		c.run(c.step(q, nil, nil))
		c.run(fixture.Prepared(c.step(q, nil, nil)))
		q = "SELECT u.* FROM (SELECT 1 AS witness) w LEFT JOIN information_schema.check_constraint_routine_usage u ON u.constraint_name='ccru_missing'"
		c.run(c.step(q, [][]any{make([]any, 6)}))
		c.run(fixture.Prepared(c.step(q, [][]any{make([]any, 6)})))
		c.missingColumn()
	})
}
