package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func securityLabelPermissionScenario(t *testing.T, smoke bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			role := "sl_actor_" + fmt.Sprint(prepared)
			fixture.RoleReferenceCleanup(t, []string{role})
			fixture.SchemaReferenceCleanup(t, "sl_private")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					bootstrap := "postgres"
					if b.PG {
						bootstrap = "stuntgres"
					}
					c, admin := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(q string) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(q, 0)) }
					run("CREATE USER " + role)
					run("CREATE SCHEMA sl_private")
					run("CREATE TABLE sl_private.t(v INT)")
					fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("INSERT INTO sl_private.t VALUES(7)", 1))
					run("COMMENT ON TABLE sl_private.t IS 'old'")
					run("COMMENT ON COLUMN sl_private.t.v IS 'old column'")
					run("CREATE SEQUENCE sl_private.seq")
					run("REVOKE ALL ON SCHEMA sl_private FROM PUBLIC")
					if smoke {
						securityLabelError(t, p, b, admin, prepared, "SECURITY LABEL ON TABLE sl_private.t IS 'classified'", securityLabelNoProvider)
						securityLabelError(t, p, b, admin, prepared, "SECURITY LABEL FOR 'MissingProvider' ON TABLE sl_private.t IS NULL", `security label provider "MissingProvider" is not loaded`)
					}
					fixture.LockExec(t, p.Ctx, c, "SET SESSION AUTHORIZATION "+role, 0)
					defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					targets := []string{"TABLE sl_private.t"}
					if !smoke { targets = append(targets, "TABLE sl_private.missing", "COLUMN sl_private.t.missing", "ROLE sl_missing", "COLUMN v", "TABLE sl_private.seq") }
					for _, target := range targets {
						for _, label := range []string{"'classified'", "NULL"} {
							securityLabelError(t, p, b, c, prepared, "SECURITY LABEL ON "+target+" IS "+label, securityLabelNoProvider)
							securityLabelError(t, p, b, c, prepared, "SECURITY LABEL FOR missing ON "+target+" IS "+label, `security label provider "missing" is not loaded`)
						}
					}
					run("GRANT USAGE ON SCHEMA sl_private TO " + role)
					securityLabelError(t, p, b, c, prepared, "SECURITY LABEL ON TABLE sl_private.t IS NULL", securityLabelNoProvider)
					securityLabelError(t, p, b, c, prepared, "SECURITY LABEL FOR missing ON TABLE sl_private.t IS 'classified'", `security label provider "missing" is not loaded`)
					fixture.LockExec(t, p.Ctx, c, "RESET SESSION AUTHORIZATION", 0)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT current_user, session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{bootstrap, bootstrap}}))
					securityLabelUnchanged(t, p, b, c, "sl_private.t")
					fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Select("SELECT has_schema_privilege('"+role+"','sl_private','USAGE') AS usage,has_schema_privilege('"+role+"','sl_private','CREATE') AS create_right", []string{"usage", "create_right"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
				})
			}
		})
	}
}

func TestReferenceSecurityLabelObjectPermissionPrecedence(t *testing.T) {
	securityLabelPermissionScenario(t, false)
}

func TestReferenceSecurityLabelFeatureSmoke(t *testing.T) {
	securityLabelPermissionScenario(t, true)
}
