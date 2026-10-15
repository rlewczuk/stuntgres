package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHypotheticalAggregateGrammar(t *testing.T) {
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		hypotheticalSetup(t, p, c, b)
		for _, tc := range []struct{ q, code, message, token string }{
			{"VALUES(rank(1) WITHIN GROUP(ORDER BY 1))", "42803", "aggregate functions are not allowed in VALUES", "rank"},
			{"INSERT INTO hs_input VALUES(rank(1) WITHIN GROUP(ORDER BY 1))", "42803", "aggregate functions are not allowed in VALUES", "rank"},
			{"INSERT INTO hs_input VALUES(1) RETURNING rank(1) WITHIN GROUP(ORDER BY v)", "42803", "aggregate functions are not allowed in RETURNING", "rank"},
			{"SELECT a.v FROM hs_input AS a JOIN hs_input AS b ON rank(1) WITHIN GROUP(ORDER BY a.v)=1", "42803", "aggregate functions are not allowed in JOIN conditions", "rank"},
			{"CREATE TABLE hs_check(v INT CHECK(rank(1) WITHIN GROUP(ORDER BY v)>0))", "42803", "aggregate functions are not allowed in check constraints", "rank"},
			{"CREATE INDEX hs_index ON hs_input ((rank(1) WITHIN GROUP(ORDER BY v)))", "42803", "aggregate functions are not allowed in index expressions", "rank"},
			{"CREATE TABLE hs_generated(v INT,r BIGINT GENERATED ALWAYS AS (rank(1) WITHIN GROUP(ORDER BY v)) STORED)", "42803", "aggregate functions are not allowed in column generation expressions", "rank"},
			{"SELECT rank(1,row_number() OVER()) WITHIN GROUP(ORDER BY v,v) FROM hs_input", "42803", "aggregate function calls cannot contain window function calls", "row_number"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := orderedSetDiagnostic(tc.q, tc.code, tc.message, tc.token, "")
				if strings.HasPrefix(tc.q, "CREATE ") {
					s.ErrorStage = "execute"
				}
				orderedSetBoth(t, p, c, b, s)
			})
		}
		orderedSetBoth(t, p, c, b, orderedSetValue("SELECT count(*) AS v FROM hs_input", "INT8", int64(4)))
		// An aggregate containing only outer input variables belongs to that
		// outer query, while a later lower-level direct variable is forbidden.
		orderedSetBoth(t, p, c, b, orderedSetValue("SELECT (SELECT rank(20) WITHIN GROUP(ORDER BY a.v)) AS v FROM hs_input AS a", "INT8", int64(2)))
		q := "SELECT (SELECT rank(20,b.v) WITHIN GROUP(ORDER BY a.v,a.v) FROM hs_input AS b) FROM hs_input AS a"
		orderedSetBoth(t, p, c, b, orderedSetDiagnostic(q, "42803", "outer-level aggregate cannot contain a lower-level variable in its direct arguments", "b.v)", ""))
		// Window overloads remain distinct and deliberately have no new native provider.
		q = "SELECT rank() OVER(ORDER BY v) AS v FROM hs_input"
		if b.PG {
			orderedSetBoth(t, p, c, b, fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(4)}}))
		} else {
			orderedSetBoth(t, p, c, b, orderedSetDiagnostic(q, "0A000", "window function provider is unsupported", "rank", ""))
		}
	})
}

func TestReferenceHypotheticalAggregatePrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"hs_reader"})
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE hs_acl(v INT,h INT)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE ROLE hs_reader", 0))
		if b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT USAGE ON SCHEMA public TO hs_reader", 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT SELECT(v) ON hs_acl TO hs_reader", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION hs_reader", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, q := range []string{
			"SELECT rank(1,h) WITHIN GROUP(ORDER BY v,v) FROM hs_acl WHERE FALSE GROUP BY h",
			"SELECT rank(1,1) WITHIN GROUP(ORDER BY v,h) FILTER(WHERE FALSE) FROM hs_acl",
			"SELECT rank(1,1) WITHIN GROUP(ORDER BY v,v) FILTER(WHERE h>0) FROM hs_acl HAVING FALSE",
			"SELECT rank(1,(SELECT h FROM hs_acl)) WITHIN GROUP(ORDER BY v,v) FROM hs_acl WHERE FALSE",
		} {
			s := orderedSetDiagnostic(q, "42501", "permission denied for table hs_acl", "", "")
			s.ErrorStage = "execute"
			orderedSetBoth(t, p, c, b, s)
		}
	})
}
