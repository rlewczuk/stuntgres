package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleSettingsActionsQuotedCaseLists(t *testing.T) {
	rsActionCases(t, []string{"Rs_Action_Mixed"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
		run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
		run(fixture.Exec(`CREATE USER "Rs_Action_Mixed" PASSWORD 'role-attributes-fixture'`, 0))
		run(rsActionError("ALTER ROLE Rs_Action_Mixed SET application_name='wrong'", "42704", `role "rs_action_mixed" does not exist`))
		run(fixture.Exec(`ALTER USER "Rs_Action_Mixed" SET "APPLICATION_NAME" = 'Mixed Value'`, 0))
		run(rsActionConfig("Rs_Action_Mixed", `{"application_name=Mixed Value"}`))
		run(fixture.Exec(`ALTER ROLE "Rs_Action_Mixed" SET search_path TO "Case Schema", public, pg_catalog`, 0))
		run(fixture.Exec(`ALTER ROLE "Rs_Action_Mixed" SET DateStyle TO ISO, MDY`, 0))
		pool := fixture.RoleAttributesPool(t, p, b, "Rs_Action_Mixed")
		login, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer login.Close()
		for _, s := range []fixture.Step{
			fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"Mixed Value"}}),
			fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{`"Case Schema", public, pg_catalog`}}),
			fixture.Select("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, MDY"}}),
		} {
			rsActionRun(t, p, b, login, route, s)
		}
		run(fixture.Exec(`ALTER USER "Rs_Action_Mixed" RESET ALL`, 0))
		run(rsActionConfig("Rs_Action_Mixed", ""))
	})
}
