package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONAuthority(t *testing.T) {
	const role = "sqj_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	fixture.SchemaReferenceCleanup(t, "sqj_private")
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		defer c.ExecContext(p.Ctx, `RESET SESSION AUTHORIZATION`)
		run(fixture.Exec("CREATE USER "+role, 0))
		run(fixture.Exec(`CREATE TABLE sqj_acl(id int,k text,v text,hidden text)`, 0))
		run(fixture.Exec(`INSERT INTO sqj_acl VALUES(1,'a','{}','secret')`, 1))
		run(fixture.Exec(`CREATE SCHEMA sqj_private`, 0))
		run(fixture.Exec(`CREATE TABLE sqj_private.docs(v text)`, 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		for _, q := range []string{
			`SELECT JSON_ARRAY(v FORMAT JSON) AS v FROM sqj_acl`,
			`SELECT JSON_OBJECT(k:v FORMAT JSON) AS v FROM sqj_acl WHERE FALSE`,
			`SELECT JSON_ARRAY(SELECT v FROM sqj_acl) AS v`,
			`SELECT JSON_SCALAR(hidden) AS v FROM sqj_acl`,
		} {
			t.Run("denied source "+q, func(t *testing.T) {
				fixture.SQLJSONDiagnostic(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table sqj_acl"), false)
			})
		}
		t.Run("denied schema", func(t *testing.T) {
			q := `SELECT JSON_ARRAY(SELECT v FROM sqj_private.docs) AS v`
			d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema sqj_private")
			d.Position = "33"
			fixture.SQLJSONDiagnostic(t, p, b, c, q, d, true)
		})
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(fixture.Exec("GRANT SELECT(id,k) ON sqj_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		for _, q := range []string{
			`SELECT JSON_OBJECT(k:v FORMAT JSON) AS v FROM sqj_acl`,
			`SELECT JSON_OBJECT(v:k) AS v FROM sqj_acl`,
			`SELECT JSON_ARRAY(SELECT i.v FROM sqj_acl i WHERE i.id=o.id) AS v FROM sqj_acl o`,
			`SELECT JSON_ARRAY(SELECT i.k FROM sqj_acl i WHERE i.id=o.id AND o.hidden IS NOT NULL) AS v FROM sqj_acl o WHERE FALSE`,
		} {
			t.Run("denied actual column "+q, func(t *testing.T) {
				fixture.SQLJSONDiagnostic(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table sqj_acl"), false)
			})
		}
		t.Run("unused column not demanded", func(t *testing.T) {
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT JSON_OBJECT(k:id) AS v FROM sqj_acl`, []string{"v"}, []string{"JSON"}, [][]any{{`{"a" : 1}`}}))
		})
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(fixture.Exec("GRANT SELECT(v) ON sqj_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		t.Run("allowed actual operands", func(t *testing.T) {
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT JSON_OBJECT(k:v FORMAT JSON) AS v FROM sqj_acl`, []string{"v"}, []string{"JSON"}, [][]any{{`{"a" : {}}`}}))
		})
		t.Run("allowed correlated query", func(t *testing.T) {
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT JSON_ARRAY(SELECT i.v FROM sqj_acl i WHERE i.id=o.id FORMAT JSON) AS v FROM sqj_acl o`, []string{"v"}, []string{"JSON"}, [][]any{{`[{}]`}}))
		})
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
	})
}
