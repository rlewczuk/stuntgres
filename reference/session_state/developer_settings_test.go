package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeveloperSettingsInventory(t *testing.T) {
	roles := []string{"developer_inventory_plain", "developer_inventory_reader", "developer_inventory_grantee"}
	physicalCases(t, roles, func(s *physicalCase) {
		for _, role := range roles {
			s.developerExec("CREATE ROLE "+role+" LOGIN PASSWORD 'role-core-fixture'", false)
		}
		s.developerExec("GRANT pg_read_all_settings TO "+roles[1], false)
		s.developerExec("GRANT SET ON PARAMETER trace_sort TO "+roles[2], false)
		defer s.conn.ExecContext(s.p.Ctx, "REVOKE SET ON PARAMETER trace_sort FROM "+roles[2])
		for _, actor := range append([]string{""}, roles...) {
			user := s
			if actor != "" {
				user = s.login(actor)
			}
			for _, prepared := range []bool{false, true} {
				for _, tc := range developerSettings {
					user.developerRead(tc, tc.initial, prepared)
					user.developerMetadata(tc, tc.initial, "default", prepared)
				}
				user.developerAll(prepared)
				for _, args := range [][]any{
					{"trace_sort", true}, {"trace_sort", false}, {"trace_sort", nil},
					{"debug_deadlocks", nil}, {nil, true}, {nil, false},
				} {
					var want any
					if args[0] == "trace_sort" && args[1] != nil {
						want = "off"
					}
					step := fixture.Select("SELECT current_setting($1,$2)", []string{"current_setting"}, []string{"TEXT"}, [][]any{{want}}, args...)
					step.Prepared = prepared
					user.run(step)
				}

				// The second formal is Boolean, including at literal coercion and
				// parameter import. These fail before any setting lookup occurs.
				badLiteral := physicalDiagnostic("22P02", `invalid input syntax for type boolean: "broken"`)
				badLiteral.Position = fixture.Position(37)
				step := physicalError("SELECT current_setting('trace_sort','broken')", badLiteral, prepared, true)
				if prepared {
					step.ErrorStage = "prepare"
				}
				user.run(step)
				badParameter := physicalDiagnostic("22P02", `invalid input syntax for type boolean: "broken"`)
				badParameter.Context = "unnamed portal parameter $2 = '...'"
				user.run(physicalError("SELECT current_setting($1,$2)", badParameter, prepared, true, "trace_sort", "broken"))

				if actor == roles[0] {
					// missing_ok suppresses unknown names, never forbidden reads.
					for _, flag := range []string{"true", "false"} {
						query := "SELECT current_setting('config_file'," + flag + ")"
						user.run(physicalError(query, physicalDenied("config_file"), prepared, true))
					}
					for _, flag := range []bool{true, false} {
						user.run(physicalError("SELECT current_setting($1,$2)", physicalDenied("config_file"), prepared, true, "config_file", flag))
					}
				}
				for _, tc := range developerSettings {
					if tc.name == "trace_sort" {
						user.developerRead(tc, "off", prepared)
						user.developerMetadata(tc, "off", "default", prepared)
					}
				}

				for _, name := range developerAbsent {
					for _, query := range []string{
						"SHOW " + name, "SELECT current_setting('" + name + "')",
						"SET " + name + "='off'", "RESET " + name,
					} {
						step := physicalError(query, developerUnknown(name), prepared, strings.HasPrefix(query, "SHOW") || strings.HasPrefix(query, "SELECT"))
						if prepared && strings.HasPrefix(query, "SHOW") {
							step.ErrorStage = "prepare"
						}
						user.run(step)
					}
					step := fixture.Select("SELECT current_setting('"+name+"',true)", []string{"current_setting"}, []string{"TEXT"}, [][]any{{nil}})
					step.Prepared = prepared
					step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						settingsTextMetadata(t, rows, user.pg, true)
					}
					user.run(step)
					user.run(physicalError("SELECT current_setting($1,$2)", developerUnknown(name), prepared, true, name, false))
					step = fixture.Select("SELECT name FROM pg_settings WHERE name='"+name+"'", []string{"name"}, []string{"TEXT"}, nil)
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

func TestReferenceDeveloperSettingsValues(t *testing.T) {
	for _, tc := range developerSettings {
		t.Run(tc.name, func(t *testing.T) {
			physicalCases(t, nil, func(s *physicalCase) {
				for _, prepared := range []bool{false, true} {
					if !developerMutable(tc) {
						for _, value := range []string{tc.alternate, "broken"} {
							for _, query := range []string{
								"SET " + tc.name + "=" + physicalLiteral(value),
								"SET " + tc.name + "=DEFAULT", "RESET " + tc.name,
							} {
								s.run(physicalError(query, developerContext(tc), prepared, false))
							}
							s.run(physicalError("SELECT set_config($1,$2,$3)", developerContext(tc), prepared, true, tc.name, value, false))
							s.developerExec("BEGIN", prepared)
							s.run(physicalError("SET LOCAL "+tc.name+"="+physicalLiteral(value), developerContext(tc), prepared, false))
							s.developerExec("ROLLBACK", prepared)
						}
						s.run(physicalError("SELECT set_config($1,$2,$3)", developerContext(tc), prepared, true, tc.name, nil, false))
						s.developerExec("RESET ALL", prepared)
						s.developerRead(tc, tc.initial, prepared)
						s.developerMetadata(tc, tc.initial, "default", prepared)
						continue
					}
					s.developerExec("SET \""+strings.ToUpper(tc.name)+"\"="+physicalLiteral(tc.alternate), prepared)
					s.developerRead(tc, tc.alternate, prepared)
					s.developerMetadata(tc, tc.alternate, "session", prepared)
					invalid := "broken"
					if tc.kind == "integer" {
						invalid = "1"
					}
					if tc.name == "backtrace_functions" {
						invalid = "foo-bar"
					}
					s.run(physicalError("SET "+tc.name+"="+physicalLiteral(invalid), developerInvalid(tc, invalid), prepared, false))
					s.developerRead(tc, tc.alternate, prepared)
					s.developerMetadata(tc, tc.alternate, "session", prepared)
					s.developerExec("SET "+tc.name+"=DEFAULT", prepared)
					s.developerRead(tc, tc.initial, prepared)
					s.developerMetadata(tc, tc.initial, "default", prepared)
					s.developerConfig(tc, tc.alternate, tc.alternate, prepared)
					s.developerRead(tc, tc.alternate, prepared)
					s.developerConfig(tc, nil, tc.initial, prepared)
					s.developerMetadata(tc, tc.initial, "default", prepared)
					s.developerExec("SET "+tc.name+"="+physicalLiteral(tc.alternate), prepared)
					s.developerExec("RESET "+tc.name, prepared)
					s.developerRead(tc, tc.initial, prepared)
					if tc.kind == "bool" {
						for _, v := range []struct{ input, want string }{
							{"Tr", "on"}, {"Y", "on"}, {"1", "on"}, {"Fa", "off"},
							{"N", "off"}, {"0", "off"}, {"ON", "on"}, {"OF", "off"},
						} {
							s.developerConfig(tc, v.input, v.want, prepared)
						}
						s.run(physicalError("SELECT set_config($1,$2,$3)", developerInvalid(tc, "o"), prepared, true, tc.name, "o", false))
					}
					if tc.kind == "enum" {
						s.developerConfig(tc, strings.ToUpper(tc.alternate), tc.alternate, prepared)
						for _, v := range []string{tc.alternate[:1], " " + tc.alternate} {
							s.run(physicalError("SELECT set_config($1,$2,$3)", developerInvalid(tc, v), prepared, true, tc.name, v, false))
						}
					}
					s.developerExec("RESET ALL", prepared)
				}
			})
		})
	}
}

func TestReferenceDeveloperSettingsSimulationSmoke(t *testing.T) {
	roles := []string{"developer_smoke_plain"}
	physicalCases(t, roles, func(s *physicalCase) {
		s.developerExec("CREATE ROLE "+roles[0]+" LOGIN PASSWORD 'role-core-fixture'", false)
		plain := s.login(roles[0])
		s.developerExec("CREATE TABLE developer_smoke(v integer)", false)
		s.run(fixture.Exec("INSERT INTO developer_smoke VALUES(1),(2),(3)", 3))
		for _, prepared := range []bool{false, true} {
			s.compatibleSentinel("developer_smoke", prepared)
			for _, tc := range developerSettings {
				if developerMutable(tc) {
					s.developerExec("SET "+tc.name+"="+physicalLiteral(tc.alternate), prepared)
					s.developerRead(tc, tc.alternate, prepared)
					s.developerMetadata(tc, tc.alternate, "session", prepared)
				} else {
					s.run(physicalError("SET "+tc.name+"="+physicalLiteral(tc.alternate), developerContext(tc), prepared, false))
					s.developerRead(tc, tc.initial, prepared)
				}
				plain.developerRead(tc, tc.initial, prepared)
				s.compatibleSentinel("developer_smoke", prepared)
			}
			for _, name := range developerAbsent {
				s.run(physicalError("SET "+name+"='on'", developerUnknown(name), prepared, false))
				s.compatibleSentinel("developer_smoke", prepared)
			}
			for _, tc := range developerSettings {
				if tc.name != "backtrace_functions" && tc.name != "wal_consistency_checking" && tc.name != "debug_discard_caches" {
					continue
				}
				value := "bogus"
				if tc.name == "backtrace_functions" {
					value = "foo-bar"
				}
				if tc.kind == "integer" {
					value = "1"
				}
				s.run(physicalError("SET "+tc.name+"="+physicalLiteral(value), developerInvalid(tc, value), prepared, false))
				plain.run(physicalError("SET "+tc.name+"="+physicalLiteral(value), developerDenied(tc.name), prepared, false))
				s.compatibleSentinel("developer_smoke", prepared)
			}
			s.developerExec("RESET ALL", prepared)
			for _, tc := range developerSettings {
				s.developerRead(tc, tc.initial, prepared)
				s.developerMetadata(tc, tc.initial, "default", prepared)
			}
			s.compatibleSentinel("developer_smoke", prepared)
		}
	})
}
