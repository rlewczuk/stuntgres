package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func compatibleSighup() fixture.Diagnostic {
	return physicalDiagnostic("55P02", `parameter "allow_alter_system" cannot be changed now`)
}

func TestReferenceCompatibleSettingsAuthority(t *testing.T) {
	t.Run("sighup", func(t *testing.T) {
		physicalCases(t, []string{"compatible_context_login"}, func(s *physicalCase) {
			s.run(fixture.Exec("CREATE ROLE compatible_context_login LOGIN PASSWORD 'role-core-fixture'", 0))
			if s.pg {
				// The fixture recreates public without its initdb USAGE grant.
				// Native public-schema access does not require this setup.
				s.run(fixture.Exec("GRANT USAGE ON SCHEMA public TO compatible_context_login", 0))
			}
			s.run(fixture.Exec("CREATE TABLE compatible_context_sentinel(v integer)", 0))
			s.run(fixture.Exec("INSERT INTO compatible_context_sentinel VALUES(1),(2),(3)", 3))
			s.run(fixture.Exec("GRANT SELECT ON compatible_context_sentinel TO compatible_context_login", 0))
			ordinary := s.login("compatible_context_login")
			for _, actor := range []*physicalCase{s, ordinary} {
				for _, prepared := range []bool{false, true} {
					for _, query := range []string{"SET allow_alter_system=on", "SET allow_alter_system=off", "SET allow_alter_system='broken'", "SET allow_alter_system=DEFAULT", "RESET allow_alter_system", "SELECT set_config('allow_alter_system','off',false)", "SELECT set_config('allow_alter_system','broken',false)", "SELECT set_config('allow_alter_system',NULL,false)"} {
						actor.run(physicalError(query, compatibleSighup(), prepared, strings.HasPrefix(query, "SELECT")))
						actor.compatibleReads(compatibleSettings[0], "on", prepared)
						actor.compatibleSentinel("compatible_context_sentinel", prepared)
					}
					actor.run(fixture.Exec("BEGIN", 0))
					actor.run(physicalError("SET LOCAL allow_alter_system='broken'", compatibleSighup(), prepared, false))
					actor.run(fixture.Exec("ROLLBACK", 0))
					actor.compatibleReads(compatibleSettings[0], "on", prepared)
					actor.compatibleSentinel("compatible_context_sentinel", prepared)
					actor.run(fixture.Exec("RESET ALL", 0))
				}
			}
			s.run(fixture.Exec("GRANT SET ON PARAMETER allow_alter_system TO compatible_context_login", 0))
			ordinary.run(physicalError("SET allow_alter_system=off", compatibleSighup(), true, false))
			ordinary.run(physicalError("SELECT set_config('allow_alter_system','broken',false)", compatibleSighup(), true, true))
			ordinary.compatibleReads(compatibleSettings[0], "on", true)
			s.run(fixture.Exec("REVOKE SET ON PARAMETER allow_alter_system FROM compatible_context_login", 0))
		})
	})
	t.Run("large-object-setting", func(t *testing.T) {
		physicalCases(t, []string{"compatible_acl_login", "compatible_acl_member"}, func(s *physicalCase) {
			s.run(fixture.Exec("CREATE ROLE compatible_acl_login LOGIN PASSWORD 'role-core-fixture'", 0))
			s.run(fixture.Exec("CREATE ROLE compatible_acl_member", 0))
			ordinary := s.login("compatible_acl_login")
			for _, tc := range compatibleSettings {
				for _, prepared := range []bool{false, true} {
					ordinary.compatibleReads(tc, tc.initial, prepared)
					ordinary.compatibleMetadata(tc, tc.initial, "default", prepared)
				}
			}
			ordinary.compatibleVisible(true)
			tc := compatibleSettings[4]
			denied := clientSetDenied(tc.name)
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{"SET lo_compat_privileges=on", "SET lo_compat_privileges='broken'", "SET lo_compat_privileges=DEFAULT", "RESET lo_compat_privileges", "SELECT set_config('lo_compat_privileges','broken',false)", "SELECT set_config('lo_compat_privileges',NULL,false)"} {
					ordinary.run(physicalError(query, denied, prepared, strings.HasPrefix(query, "SELECT")))
					ordinary.compatibleReads(tc, "off", prepared)
				}
			}
			s.run(fixture.Exec("SET lo_compat_privileges=on", 0))
			s.compatibleReads(tc, "on", true)
			s.run(fixture.Exec("RESET lo_compat_privileges", 0))
			query := "SET lo_compat_privileges=on"
			held, err := ordinary.conn.PrepareContext(s.p.Ctx, query)
			if err != nil {
				s.t.Fatal(err)
			}
			defer held.Close()
			ordinary.compatibleReads(tc, "off", false)
			for _, grantee := range []string{"compatible_acl_login", "compatible_acl_member"} {
				if grantee == "compatible_acl_member" {
					s.run(fixture.Exec("GRANT compatible_acl_member TO compatible_acl_login", 0))
				}
				s.run(fixture.Exec("GRANT SET ON PARAMETER lo_compat_privileges TO "+grantee, 0))
				fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: ordinary.conn, Statement: held, SQL: query}, ordinary.recorder, fixture.Exec(query, 0))
				ordinary.compatibleReads(tc, "on", true)
				if grantee == "compatible_acl_member" {
					ordinary.run(fixture.Exec("SET ROLE compatible_acl_member", 0))
					ordinary.run(fixture.Exec("SET lo_compat_privileges=on", 0))
					ordinary.run(fixture.Exec("RESET ROLE", 0))
				}
				s.run(fixture.Exec("REVOKE SET ON PARAMETER lo_compat_privileges FROM "+grantee, 0))
				fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: ordinary.conn, Statement: held, SQL: query}, ordinary.recorder, physicalError(query, denied, false, false))
				ordinary.run(physicalError("RESET lo_compat_privileges", denied, true, false))
				ordinary.run(fixture.Exec("BEGIN", 0))
				ordinary.run(fixture.Exec("RESET ALL", 0))
				ordinary.compatibleReads(tc, "off", false)
				ordinary.run(fixture.Exec("ROLLBACK", 0))
				ordinary.compatibleReads(tc, "on", false)
				ordinary.run(fixture.Exec("RESET ALL", 0))
				ordinary.compatibleReads(tc, "off", true)
			}
			for _, grant := range []string{"ALTER SYSTEM ON PARAMETER lo_compat_privileges", "pg_read_all_settings"} {
				s.run(fixture.Exec("GRANT "+grant+" TO compatible_acl_login", 0))
				ordinary.run(physicalError(query, denied, true, false))
				ordinary.compatibleReads(tc, "off", true)
				s.run(fixture.Exec("REVOKE "+grant+" FROM compatible_acl_login", 0))
			}
		})
	})
}
