package reference_test

import (
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLoggingSettingsInventory(t *testing.T) {
	roles := []string{"logging_inventory_plain", "logging_inventory_reader", "logging_inventory_setter"}
	physicalCases(t, roles, func(s *physicalCase) {
		for _, role := range roles {
			s.developerExec("CREATE ROLE "+role+" LOGIN PASSWORD 'role-core-fixture'", false)
		}
		s.developerExec("GRANT pg_read_all_settings TO "+roles[1], false)
		s.developerExec("GRANT SET ON PARAMETER log_directory,log_filename TO "+roles[2], false)
		defer s.conn.ExecContext(s.p.Ctx, "REVOKE SET ON PARAMETER log_directory,log_filename FROM "+roles[2])
		if s.pg {
			var file string
			var line int64
			if err := s.conn.QueryRowContext(s.p.Ctx, "SELECT sourcefile,sourceline FROM pg_settings WHERE name='log_timezone'").Scan(&file, &line); err != nil {
				s.t.Fatal("missing configured timezone provenance", err)
			}
			if !strings.HasPrefix(file, "/") || !strings.HasSuffix(file, "/postgresql.conf") || line <= 0 {
				s.t.Fatal("invalid configured timezone provenance", file, line)
			}
		}
		for _, actor := range append([]string{""}, roles...) {
			user := s
			if actor != "" {
				user = s.login(actor)
			}
			allowed := actor == "" || actor == roles[1]
			for _, prepared := range []bool{false, true} {
				for _, tc := range loggingSettings {
					initial, raw, source := user.loggingInitial(tc)
					if loggingRestricted(tc) && !allowed {
						for _, requested := range []string{tc.name, strings.ToUpper(tc.name)} {
							step := physicalError("SHOW \""+requested+"\"", physicalDenied(requested), prepared, true)
							if prepared {
								step.ErrorStage = "prepare"
							}
							user.run(step)
							for _, flag := range []bool{false, true} {
								user.run(physicalError("SELECT current_setting($1,$2)", physicalDenied(requested), prepared, true, requested, flag))
							}
						}
						step := fixture.Select("SELECT name FROM pg_settings WHERE name='"+tc.name+"'", []string{"name"}, []string{"TEXT"}, nil)
						step.Prepared = prepared
						user.run(step)
						continue
					}
					user.loggingRead(tc, initial, prepared)
					user.loggingMetadata(tc, raw, source, prepared)
				}
				user.loggingAll(allowed, prepared)
				for _, args := range [][]any{{"log_duration", true}, {"log_duration", false}, {"log_duration", nil}, {nil, true}, {nil, false}} {
					var want any
					if args[0] != nil && args[1] != nil {
						want = "off"
					}
					step := fixture.Select("SELECT current_setting($1,$2)", []string{"current_setting"}, []string{"TEXT"}, [][]any{{want}}, args...)
					step.Prepared = prepared
					user.run(step)
				}
			}
			if actor != "" {
				user.conn.Close()
			}
		}
	})
}

func TestReferenceLoggingSettingsValues(t *testing.T) {
	for _, tc := range loggingSettings {
		t.Run(tc.name, func(t *testing.T) {
			physicalCases(t, nil, func(s *physicalCase) {
				initial, raw, source := s.loggingInitial(tc)
				for _, prepared := range []bool{false, true} {
					if !loggingMutable(tc) {
						for _, value := range []string{tc.alternate, initial, "broken"} {
							for _, query := range []string{"SET " + tc.name + "=" + physicalLiteral(value), "SET " + tc.name + "=DEFAULT", "RESET " + tc.name} {
								s.run(physicalError(query, loggingContext(tc), prepared, false))
							}
							for _, local := range []bool{false, true} {
								s.developerExec("BEGIN", prepared)
								s.run(physicalError("SELECT set_config($1,$2,$3)", loggingContext(tc), prepared, true, tc.name, value, local))
								s.developerExec("ROLLBACK", prepared)
							}
							s.developerExec("BEGIN", prepared)
							s.run(physicalError("SET LOCAL "+tc.name+"="+physicalLiteral(value), loggingContext(tc), prepared, false))
							s.developerExec("ROLLBACK", prepared)
						}
						for _, local := range []bool{false, true} {
							s.run(physicalError("SELECT set_config($1,$2,$3)", loggingContext(tc), prepared, true, tc.name, nil, local))
						}
						s.developerExec("RESET ALL", prepared)
						s.loggingRead(tc, initial, prepared)
						s.loggingMetadata(tc, raw, source, prepared)
						continue
					}
					// Preparing a setter is inert; the held reader observes execution time.
					stmt, err := s.conn.PrepareContext(s.p.Ctx, "SET "+tc.name+"="+physicalLiteral(tc.alternate))
					if err != nil {
						s.t.Fatal(err)
					}
					s.loggingRead(tc, initial, prepared)
					stmt.Close()
					s.developerExec("SET \""+strings.ToUpper(tc.name)+"\"="+physicalLiteral(tc.alternate), prepared)
					s.loggingRead(tc, tc.show, prepared)
					s.loggingMetadata(tc, tc.raw, "session", prepared)
					if tc.kind != "string" {
						s.run(physicalError("SET "+tc.name+"='broken'", loggingInvalid(tc, "broken"), prepared, false))
						step := physicalError("SELECT set_config($1,$2,$3)", loggingInvalid(tc, "broken"), prepared, true, tc.name, "broken", false)
						if tc.name == "log_parameter_max_length_on_error" {
							// PostgreSQL reports bound values once error logging is enabled;
							// the native setting-only simulation leaves Context empty.
							pg := loggingInvalid(tc, "broken")
							pg.Context = "unnamed portal with parameters: $1 = 'log_parameter_max_length_on_error', $2 = 'broken', $3 = 'false'"
							step.PostgresDiagnostic = &pg
						}
						s.run(step)
						s.loggingRead(tc, tc.show, prepared)
						s.loggingMetadata(tc, tc.raw, "session", prepared)
					}
					s.developerExec("SET "+tc.name+"=DEFAULT", prepared)
					s.loggingRead(tc, initial, prepared)
					s.loggingConfig(tc, tc.alternate, tc.show, false, prepared)
					s.loggingConfig(tc, nil, initial, false, prepared)
					s.loggingMetadata(tc, raw, source, prepared)
					s.developerExec("SET "+tc.name+"="+physicalLiteral(tc.alternate), prepared)
					s.developerExec("RESET "+tc.name, prepared)
					s.loggingRead(tc, initial, prepared)
					s.developerExec("BEGIN", prepared)
					s.loggingConfig(tc, tc.alternate, tc.show, true, prepared)
					s.loggingRead(tc, tc.show, prepared)
					s.developerExec("COMMIT", prepared)
					s.loggingRead(tc, initial, prepared)
					if tc.kind == "bool" {
						for _, v := range []struct{ input, want string }{{"Tr", "on"}, {"Y", "on"}, {"1", "on"}, {"Fa", "off"}, {"N", "off"}, {"0", "off"}, {"ON", "on"}, {"OF", "off"}} {
							s.loggingConfig(tc, v.input, v.want, false, prepared)
						}
						s.run(physicalError("SET "+tc.name+"='o'", loggingInvalid(tc, "o"), prepared, false))
					}
					if tc.kind == "enum" {
						for _, value := range []string{tc.show[:1], " " + tc.show} {
							s.run(physicalError("SELECT set_config($1,$2,$3)", loggingInvalid(tc, value), prepared, true, tc.name, value, false))
						}
						if tc.name == "log_min_messages" || tc.name == "log_min_error_statement" {
							s.loggingConfig(tc, "debug", "debug2", false, prepared)
						}
					}
					if tc.kind == "integer" || tc.kind == "real" {
						s.loggingNumberCases(tc, prepared)
					}
					s.developerExec("RESET ALL", prepared)
					s.loggingRead(tc, initial, prepared)
					s.loggingMetadata(tc, raw, source, prepared)
				}
			})
		})
	}
}

func TestReferenceLoggingSettingsStrings(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		t := s.t
		var notices []fixture.Diagnostic
		_, conn, recorder := fixture.NoticeReferencePool(t, s.p, s.pg, &notices)
		defer conn.Close()
		actor := &physicalCase{t, s.p, conn, s.pg, recorder}
		for _, prepared := range []bool{false, true} {
			actor.developerExec("SET client_min_messages=notice", prepared)
			for _, tc := range []struct{ input, want string }{{strings.Repeat("a", 64), strings.Repeat("a", 63)}, {strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)}, {"é", `\xc3\xa9`}, {"x\ny", `x\x0ay`}, {`x\y`, `x\y`}} {
				step := fixture.Select("SELECT set_config('application_name',$1,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.input)
				step.Prepared = prepared
				var want []fixture.Diagnostic
				if len(tc.input) > 63 {
					want = []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + tc.input + `" will be truncated to "` + tc.want + `"`}}
				}
				notices = nil
				actor.run(step)
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("application name notices got %#v want %#v", notices, want)
				}
			}
			actor.developerExec("SET client_min_messages=warning", prepared)
			step := fixture.Select("SELECT set_config('application_name',$1,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{strings.Repeat("z", 63)}}, strings.Repeat("z", 64))
			step.Prepared = prepared
			notices = nil
			actor.run(step)
			if len(notices) != 0 {
				t.Fatal("prior client filter", notices)
			}
			for _, tc := range loggingSettings {
				query := "SET " + tc.name + "='a','b'"
				d := physicalDiagnostic("22023", "SET "+tc.name+" takes only one argument")
				if tc.name == "log_connections" || tc.name == "log_destination" {
					d = loggingContext(tc)
				}
				actor.run(physicalError(query, d, prepared, false))
			}
			actor.run(fixture.Select("SHOW log_file_mode", []string{"log_file_mode"}, []string{"TEXT"}, [][]any{{"0600"}}))
			actor.run(fixture.Select("SELECT setting,boot_val,reset_val,min_val,max_val FROM pg_settings WHERE name='log_file_mode'", []string{"setting", "boot_val", "reset_val", "min_val", "max_val"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"0600", "384", "384", "0", "511"}}))
			actor.developerExec("RESET ALL", prepared)
		}
		// A prepared read is evaluated at execution, not preparation.
		stmt, err := actor.conn.PrepareContext(s.p.Ctx, "SELECT current_setting('log_statement')")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		actor.developerExec("SET log_statement='all'", false)
		fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: "SELECT current_setting('log_statement')"}, recorder, fixture.Select("SELECT current_setting('log_statement')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{"all"}}))
		actor.developerExec("RESET ALL", false)
	})
}
