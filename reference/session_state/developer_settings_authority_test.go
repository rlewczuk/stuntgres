package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceDeveloperSettingsAuthority(t *testing.T) {
	roles := []string{"developer_plain", "developer_bridge", "developer_inherit", "developer_setonly", "developer_super", "developer_supermember", "developer_read", "developer_alter"}
	physicalCases(t, roles, func(s *physicalCase) {
		for _, role := range roles {
			attrs := " LOGIN PASSWORD 'role-core-fixture'"
			if role == "developer_super" {
				attrs += " SUPERUSER"
			}
			s.developerExec("CREATE ROLE "+role+attrs, false)
		}
		for _, query := range []string{
			"GRANT developer_bridge TO developer_inherit",
			"GRANT developer_bridge TO developer_setonly WITH INHERIT FALSE, SET TRUE",
			"GRANT developer_super TO developer_supermember",
			"GRANT pg_read_all_settings TO developer_read",
		} {
			s.developerExec(query, false)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, tc := range developerSettings {
				for _, role := range []string{"developer_plain", "developer_bridge", "PUBLIC"} {
					if _, err := s.conn.ExecContext(ctx, "REVOKE SET ON PARAMETER "+tc.name+" FROM "+role); err != nil {
						s.t.Error(err)
					}
				}
				if _, err := s.conn.ExecContext(ctx, "REVOKE ALTER SYSTEM ON PARAMETER "+tc.name+" FROM developer_alter"); err != nil {
					s.t.Error(err)
				}
			}
		}()
		plain := s.login("developer_plain")
		inherited := s.login("developer_inherit")
		setonly := s.login("developer_setonly")
		super := s.login("developer_super")
		for _, tc := range developerSettings {
			if tc.context != "superuser" {
				continue
			}
			s.developerExec("GRANT ALTER SYSTEM ON PARAMETER "+tc.name+" TO developer_alter", false)
			for _, actor := range []string{"developer_supermember", "developer_read", "developer_alter"} {
				user := s.login(actor)
				for _, prepared := range []bool{false, true} {
					user.developerRead(tc, tc.initial, prepared)
					user.run(physicalError("SET "+tc.name+"='broken'", developerDenied(tc.name), prepared, false))
				}
				user.conn.Close()
			}
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{"SET " + tc.name + "='broken'", "RESET " + tc.name} {
					plain.run(physicalError(query, developerDenied(tc.name), prepared, false))
				}
				plain.run(physicalError("SELECT set_config($1,$2,$3)", developerDenied(tc.name), prepared, true, tc.name, tc.alternate, false))
				super.developerExec("SET "+tc.name+"="+physicalLiteral(tc.alternate), prepared)
				super.developerRead(tc, tc.alternate, prepared)
				super.developerExec("RESET "+tc.name, prepared)
			}
			stmt, err := plain.conn.PrepareContext(s.p.Ctx, "SELECT set_config($1,$2,$3)")
			if err != nil {
				s.t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: plain.conn, Statement: stmt, SQL: "SELECT set_config($1,$2,$3)"}
			runHeld := func(allowed bool) {
				var step fixture.Step
				if allowed {
					step = fixture.Select(held.SQL, []string{"set_config"}, []string{"TEXT"}, [][]any{{tc.alternate}}, tc.name, tc.alternate, false)
				} else {
					step = physicalError(held.SQL, developerDenied(tc.name), false, true, tc.name, tc.alternate, false)
				}
				fixture.RunStep(s.t, s.p.Ctx, held, plain.recorder, step)
			}
			runHeld(false)
			s.developerExec("GRANT SET ON PARAMETER "+tc.name+" TO developer_plain", false)
			runHeld(true)
			invalid := "broken"
			if tc.kind == "integer" {
				invalid = "1"
			}
			if tc.name == "backtrace_functions" {
				invalid = "foo-bar"
			}
			plain.run(physicalError("SELECT set_config($1,$2,$3)", developerInvalid(tc, invalid), true, true, tc.name, invalid, false))
			s.developerExec("REVOKE SET ON PARAMETER "+tc.name+" FROM developer_plain", false)
			runHeld(false)
			stmt.Close()
			plain.run(physicalError("RESET "+tc.name, developerDenied(tc.name), true, false))
			plain.developerExec("RESET ALL", true)
			plain.developerRead(tc, tc.initial, true)
			s.developerExec("GRANT SET ON PARAMETER "+tc.name+" TO developer_bridge", false)
			inherited.developerConfig(tc, tc.alternate, tc.alternate, true)
			inherited.developerExec("RESET ALL", true)
			setonly.run(physicalError("SET "+tc.name+"='broken'", developerDenied(tc.name), true, false))
			setonly.developerExec("SET ROLE developer_bridge", true)
			setonly.developerConfig(tc, tc.alternate, tc.alternate, true)
			setonly.developerExec("RESET ALL", true)
			setonly.developerExec("RESET ROLE", true)
			s.developerExec("REVOKE SET ON PARAMETER "+tc.name+" FROM developer_bridge", false)
			s.developerExec("GRANT SET ON PARAMETER "+tc.name+" TO PUBLIC", false)
			plain.developerConfig(tc, tc.alternate, tc.alternate, false)
			s.developerExec("REVOKE SET ON PARAMETER "+tc.name+" FROM PUBLIC", false)
			plain.developerExec("RESET ALL", false)
		}
		for _, tc := range developerSettings {
			if developerMutable(tc) {
				continue
			}
			s.developerExec("GRANT SET ON PARAMETER "+tc.name+" TO developer_plain", false)
			for _, prepared := range []bool{false, true} {
				plain.run(physicalError("SET "+tc.name+"='broken'", developerContext(tc), prepared, false))
				plain.run(physicalError("RESET "+tc.name, developerContext(tc), prepared, false))
				plain.run(physicalError("SELECT set_config($1,$2,$3)", developerContext(tc), prepared, true, tc.name, nil, false))
				plain.developerExec("RESET ALL", prepared)
				plain.developerRead(tc, tc.initial, prepared)
			}
		}
	})
}
