package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceLoggingSettingsAuthority(t *testing.T) {
	roles := []string{"logging_plain", "logging_bridge", "logging_inherit", "logging_setonly", "logging_super", "logging_supermember", "logging_read", "logging_alter"}
	physicalCases(t, roles, func(s *physicalCase) {
		for _, role := range roles {
			attrs := " LOGIN PASSWORD 'role-core-fixture'"
			if role == "logging_super" {
				attrs += " SUPERUSER"
			}
			s.developerExec("CREATE ROLE "+role+attrs, false)
		}
		for _, query := range []string{
			"GRANT logging_bridge TO logging_inherit",
			"GRANT logging_bridge TO logging_setonly WITH INHERIT FALSE, SET TRUE",
			"GRANT logging_super TO logging_supermember",
			"GRANT pg_read_all_settings TO logging_read",
		} {
			s.developerExec(query, false)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, tc := range loggingSettings {
				for _, role := range []string{"logging_plain", "logging_bridge", "PUBLIC"} {
					if _, err := s.conn.ExecContext(ctx, "REVOKE SET ON PARAMETER "+tc.name+" FROM "+role); err != nil {
						s.t.Error(err)
					}
				}
				if _, err := s.conn.ExecContext(ctx, "REVOKE ALTER SYSTEM ON PARAMETER "+tc.name+" FROM logging_alter"); err != nil {
					s.t.Error(err)
				}
			}
		}()
		plain := s.login("logging_plain")
		inherited := s.login("logging_inherit")
		setonly := s.login("logging_setonly")
		super := s.login("logging_super")
		for _, tc := range loggingSettings {
			if tc.context != "superuser" {
				continue
			}
			s.developerExec("GRANT ALTER SYSTEM ON PARAMETER "+tc.name+" TO logging_alter", false)
			for _, actor := range []string{"logging_supermember", "logging_read", "logging_alter"} {
				user := s.login(actor)
				for _, prepared := range []bool{false, true} {
					user.loggingRead(tc, tc.initial, prepared)
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
				super.loggingRead(tc, tc.show, prepared)
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
					step = fixture.Select(held.SQL, []string{"set_config"}, []string{"TEXT"}, [][]any{{tc.show}}, tc.name, tc.alternate, false)
				} else {
					step = physicalError(held.SQL, developerDenied(tc.name), false, true, tc.name, tc.alternate, false)
				}
				fixture.RunStep(s.t, s.p.Ctx, held, plain.recorder, step)
			}
			runHeld(false)
			s.developerExec("GRANT SET ON PARAMETER "+tc.name+" TO logging_plain", false)
			runHeld(true)
			invalid := "broken"
			plain.run(physicalError("SELECT set_config($1,$2,$3)", loggingInvalid(tc, invalid), true, true, tc.name, invalid, false))
			s.developerExec("REVOKE SET ON PARAMETER "+tc.name+" FROM logging_plain", false)
			runHeld(false)
			stmt.Close()
			plain.run(physicalError("RESET "+tc.name, developerDenied(tc.name), true, false))
			plain.developerExec("RESET ALL", true)
			plain.loggingRead(tc, tc.initial, true)
			s.developerExec("GRANT SET ON PARAMETER "+tc.name+" TO logging_bridge", false)
			inherited.loggingConfig(tc, tc.alternate, tc.show, false, true)
			inherited.developerExec("RESET ALL", true)
			setonly.run(physicalError("SET "+tc.name+"='broken'", developerDenied(tc.name), true, false))
			setonly.developerExec("SET ROLE logging_bridge", true)
			setonly.loggingConfig(tc, tc.alternate, tc.show, false, true)
			setonly.developerExec("RESET ALL", true)
			setonly.developerExec("RESET ROLE", true)
			s.developerExec("REVOKE SET ON PARAMETER "+tc.name+" FROM logging_bridge", false)
			s.developerExec("GRANT SET ON PARAMETER "+tc.name+" TO PUBLIC", false)
			plain.loggingConfig(tc, tc.alternate, tc.show, false, false)
			s.developerExec("REVOKE SET ON PARAMETER "+tc.name+" FROM PUBLIC", false)
			plain.developerExec("RESET ALL", false)
		}
		for _, tc := range loggingSettings {
			if loggingMutable(tc) {
				continue
			}
			s.developerExec("GRANT SET ON PARAMETER "+tc.name+" TO logging_plain", false)
			for _, prepared := range []bool{false, true} {
				plain.run(physicalError("SET "+tc.name+"='broken'", loggingContext(tc), prepared, false))
				plain.run(physicalError("RESET "+tc.name, loggingContext(tc), prepared, false))
				plain.run(physicalError("SELECT set_config($1,$2,$3)", loggingContext(tc), prepared, true, tc.name, nil, false))
				plain.developerExec("RESET ALL", prepared)
				if !loggingRestricted(tc) {
					initial, _, _ := plain.loggingInitial(tc)
					plain.loggingRead(tc, initial, prepared)
				}
			}
		}
		// Restricted SHOW binds read authority at prepare; current_setting must
		// recheck it at every held execution, independently of parameter SET.
		for _, name := range []string{"log_directory", "log_filename"} {
			stmt, err := plain.conn.PrepareContext(s.p.Ctx, "SELECT current_setting($1,true)")
			if err != nil {
				s.t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: plain.conn, Statement: stmt, SQL: "SELECT current_setting($1,true)"}
			fixture.RunStep(s.t, s.p.Ctx, held, plain.recorder, physicalError(held.SQL, physicalDenied(name), false, true, name))
			s.developerExec("GRANT pg_read_all_settings TO logging_plain", false)
			initial := "log"
			if name == "log_filename" {
				initial = "postgresql-%Y-%m-%d_%H%M%S.log"
			}
			fixture.RunStep(s.t, s.p.Ctx, held, plain.recorder, fixture.Select(held.SQL, []string{"current_setting"}, []string{"TEXT"}, [][]any{{initial}}, name))
			s.developerExec("REVOKE pg_read_all_settings FROM logging_plain", false)
			fixture.RunStep(s.t, s.p.Ctx, held, plain.recorder, physicalError(held.SQL, physicalDenied(name), false, true, name))
			stmt.Close()
		}
		plain.developerExec("SET log_parameter_max_length_on_error='1kB'", true)
		plain.run(fixture.Select("SHOW log_parameter_max_length_on_error", []string{"log_parameter_max_length_on_error"}, []string{"TEXT"}, [][]any{{"1kB"}}))
	})
}
