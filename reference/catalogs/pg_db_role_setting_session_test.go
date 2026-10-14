package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGDBRoleSettingSessionVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"dbrole18_reader"})
	pgDBRoleSettingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		owner, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer owner.ExecContext(p.Ctx, "ROLLBACK")
		defer owner.ExecContext(p.Ctx, "RESET ROLE")
		q := "SELECT * FROM pg_db_role_setting"
		stmt, err := owner.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: owner, Statement: stmt, SQL: q}
		probe := func() {
			fixture.RunStep(t, p.Ctx, owner, b.Recorder, pgDBRoleSettingEmpty(q, b.PG))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgDBRoleSettingEmpty(q, b.PG))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, pgDBRoleSettingEmpty(q, b.PG))
		}
		exec := func(q string) {
			fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec(q, 0))
			probe()
		}
		show := func(name, value string) {
			fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Select("SHOW "+name, []string{name}, []string{"TEXT"}, [][]any{{value}}))
		}
		probe()
		exec("SET application_name='dbrole18_session'")
		show("application_name", "dbrole18_session")
		exec("SET dbrole18.custom='session'")
		show("dbrole18.custom", "session")
		exec("RESET dbrole18.custom")
		show("dbrole18.custom", "")
		exec("RESET ALL")
		exec("SET application_name='dbrole18_base'")
		exec("BEGIN")
		exec("SET LOCAL application_name='dbrole18_local'")
		show("application_name", "dbrole18_local")
		exec("SAVEPOINT dbrole18_sp")
		exec("SET application_name='dbrole18_savepoint'")
		show("application_name", "dbrole18_savepoint")
		exec("ROLLBACK TO SAVEPOINT dbrole18_sp")
		show("application_name", "dbrole18_local")
		exec("COMMIT")
		show("application_name", "dbrole18_base")
		exec("BEGIN")
		exec("SET application_name='dbrole18_rollback'")
		show("application_name", "dbrole18_rollback")
		exec("ROLLBACK")
		show("application_name", "dbrole18_base")
		exec("CREATE ROLE dbrole18_reader")
		exec("SET ROLE dbrole18_reader")
		exec("RESET ROLE")
		exec("RESET ALL")
	})
}
