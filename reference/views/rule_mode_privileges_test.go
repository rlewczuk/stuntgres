package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRuleModePrivileges(t *testing.T) {
	for _, cell := range []string{"owner", "dml", "setter", "held-transfer", "active-action-authority", "owner-action"} {
		for _, held := range []bool{false, true} {
			t.Run(cell+map[bool]string{false: "/direct", true: "/held"}[held], func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"rm_owner", "rm_dml", "rm_setter"})
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						c := ruleModeConn(t, p, b)
						run := transactionalRulesRun(t, p, b, c, nil)
						for _, q := range []string{"CREATE ROLE rm_owner", "CREATE ROLE rm_dml", "CREATE ROLE rm_setter", "GRANT CREATE,USAGE ON SCHEMA public TO rm_owner"} {
							run(fixture.Exec(q, 0))
						}
						run(fixture.Exec("GRANT USAGE ON SCHEMA public TO rm_dml,rm_setter", 0))
						ruleModeSetup(run)
						run(fixture.Exec("ALTER TABLE src OWNER TO rm_owner", 0))
						run(fixture.Exec("GRANT SELECT,INSERT,UPDATE,DELETE ON src TO rm_dml", 0))
						run(fixture.Exec("GRANT SELECT ON sink1 TO rm_owner,rm_dml,rm_setter", 0))
						run(fixture.Exec("GRANT SET ON PARAMETER session_replication_role TO rm_setter", 0))
						control := p.Native
						if b.PG {
							control = fixture.Admin
						}
						t.Cleanup(func() {
							if _, err := control.ExecContext(p.Ctx, "REVOKE SET ON PARAMETER session_replication_role FROM rm_setter"); err != nil {
								t.Error(err)
							}
						})
						q := "ALTER TABLE src DISABLE RULE r"
						h := transactionalRulesHandles(t, p, c, held, q)
						switch cell {
						case "owner":
							run(fixture.Exec("SET ROLE rm_owner", 0))
							for _, mode := range ruleModeCases {
								transactionalViewsExec(t, p, b, c, held, fixture.Exec("ALTER TABLE src "+mode.action+" r", 0))
								run(ruleModeCatalog(mode.code))
							}
							run(ruleError("SET session_replication_role=replica", "42501", "permission denied to set parameter \"session_replication_role\""))
						case "dml", "setter":
							role := "rm_dml"
							if cell == "setter" {
								role = "rm_setter"
							}
							run(fixture.Exec("SET ROLE "+role, 0))
							if cell == "setter" {
								run(fixture.Exec("SET session_replication_role=replica", 0))
							}
							for _, name := range []string{"r", "missing"} {
								s := ruleError("ALTER TABLE src DISABLE RULE "+name, "42501", "must be owner of table src")
								s.ErrorStage = "execute"
								transactionalViewsExec(t, p, b, c, held, s)
								run(ruleModeCatalog("O"))
							}
						case "held-transfer":
							run(fixture.Exec("SET ROLE rm_owner", 0))
							run = transactionalRulesRun(t, p, b, c, h)
							run(fixture.Exec(q, 0))
							run(fixture.Exec("RESET ROLE", 0))
							run(fixture.Exec("ALTER TABLE src OWNER TO rm_dml", 0))
							run(fixture.Exec("SET ROLE rm_owner", 0))
							run(ruleError(q, "42501", "must be owner of table src"))
							run(ruleModeCatalog("D"))
						case "owner-action":
							run(fixture.Exec("GRANT INSERT ON sink1 TO rm_owner", 0))
							run(fixture.Exec("SET ROLE rm_dml", 0))
							run(fixture.Exec("INSERT INTO src VALUES(2,20)", 1))
						case "active-action-authority":
							// The event owner must have action rights only for active rules.
							run(fixture.Exec("SET ROLE rm_dml", 0))
							run(ruleError("INSERT INTO src VALUES(1,10)", "42501", "permission denied for table sink1"))
							run(fixture.Exec("RESET ROLE", 0))
							run(fixture.Exec("ALTER TABLE src DISABLE RULE r", 0))
							run(fixture.Exec("SET ROLE rm_dml", 0))
							run(fixture.Exec("INSERT INTO src VALUES(2,20)", 1))
						}
						run(fixture.Exec("RESET ROLE", 0))
						run(fixture.Exec("RESET session_replication_role", 0))
						if cell == "active-action-authority" || cell == "owner-action" {
							run(ruleModeValueRows("src", []any{int64(2), int64(20)}))
						} else {
							run(ruleModeCount("src", 0))
						}
						if cell == "owner-action" {
							run(ruleModeValueRows("sink1", []any{int64(2), int64(20)}))
						} else {
							run(ruleModeCount("sink1", 0))
						}
					})
				}
			})
		}
	}
}
