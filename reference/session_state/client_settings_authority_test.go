package reference_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func clientSetDenied(name string) fixture.Diagnostic {
	return physicalDiagnostic("42501", fmt.Sprintf("permission denied to set parameter %q", name))
}

func TestReferenceClientSettingsAuthority(t *testing.T) {
	for _, tc := range clientSettings {
		t.Run(tc.name, func(t *testing.T) {
			physicalCases(t, []string{"client_setting_login", "client_setting_grantee"}, func(s *physicalCase) {
				s.clientConfiguration(tc)
				s.run(fixture.Exec("CREATE ROLE client_setting_login LOGIN PASSWORD 'role-core-fixture'", 0))
				s.run(fixture.Exec("CREATE ROLE client_setting_grantee", 0))
				if tc.name == "default_text_search_config" {
					s.run(fixture.Exec("GRANT USAGE ON SCHEMA public TO client_setting_login", 0))
				}
				ordinary := s.login("client_setting_login")
				for _, prepared := range []bool{false, true} {
					if clientRestricted(tc.name) {
						for _, query := range []string{"SHOW " + tc.name, "SELECT current_setting(" + physicalLiteral(tc.name) + ")"} {
							step := physicalError(query, physicalDenied(tc.name), prepared, true)
							if prepared && query[:4] == "SHOW" {
								step.ErrorStage = "prepare"
							}
							ordinary.run(step)
						}
						step := fixture.Select("SELECT name FROM pg_settings WHERE name="+physicalLiteral(tc.name), []string{"name"}, []string{"TEXT"}, nil)
						step.Prepared = prepared
						ordinary.run(step)
					} else {
						ordinary.clientReads(tc, clientInitial(tc, s.pg), prepared)
						step := fixture.Select("SELECT sourcefile,sourceline FROM pg_settings WHERE name="+physicalLiteral(tc.name), []string{"sourcefile", "sourceline"}, []string{"TEXT", "INT4"}, [][]any{{nil, nil}})
						step.Prepared = prepared
						ordinary.run(step)
					}
				}
				if clientContext(tc.name) == "postmaster" {
					for _, actor := range []*physicalCase{s, ordinary} {
						for _, prepared := range []bool{false, true} {
							for _, query := range []string{"SET " + tc.name + "=" + physicalLiteral(tc.initial), "SET " + tc.name + "='broken'", "SET " + tc.name + "=DEFAULT", "RESET " + tc.name, "SELECT set_config(" + physicalLiteral(tc.name) + ",NULL,false)", "SELECT set_config(" + physicalLiteral(tc.name) + ",'broken',false)"} {
								actor.run(physicalError(query, physicalRestart(tc.name), prepared, false))
							}
							actor.run(fixture.Exec("BEGIN", 0))
							actor.run(physicalError("SET LOCAL "+tc.name+"='broken'", physicalRestart(tc.name), prepared, false))
							actor.run(fixture.Exec("ROLLBACK", 0))
						}
					}
					s.run(fixture.Exec("GRANT pg_read_all_settings TO client_setting_login", 0))
					ordinary.clientReads(tc, clientInitial(tc, s.pg), true)
					ordinary.run(physicalError("RESET "+tc.name, physicalRestart(tc.name), true, false))
					s.run(fixture.Exec("REVOKE pg_read_all_settings FROM client_setting_login", 0))
					return
				}
				if clientContext(tc.name) == "user" {
					ordinary.run(fixture.Exec("SET "+tc.name+"="+physicalLiteral(tc.alternate), 0))
					return
				}
				for _, prepared := range []bool{false, true} {
					ordinary.run(physicalError("SET "+tc.name+"='broken'", clientSetDenied(tc.name), prepared, false))
					ordinary.run(physicalError("SELECT set_config("+physicalLiteral(tc.name)+",'broken',false)", clientSetDenied(tc.name), prepared, true))
				}
				query := "SET " + tc.name + "=" + physicalLiteral(tc.alternate)
				held, err := ordinary.conn.PrepareContext(s.p.Ctx, query)
				if err != nil {
					s.t.Fatal(err)
				}
				defer held.Close()
				// One eligible sensitive parameter distinguishes read authority
				// from SET authority using the same prepared metadata consumer.
				metadata := func(visible bool) {}
				if tc.name == "session_preload_libraries" {
					const metadataSQL = "SELECT name,setting,source,reset_val FROM pg_settings WHERE name='session_preload_libraries'"
					reader, err := ordinary.conn.PrepareContext(s.p.Ctx, metadataSQL)
					if err != nil {
						s.t.Fatal(err)
					}
					defer reader.Close()
					metadata = func(visible bool) {
						var want [][]any
						if visible {
							want = [][]any{{tc.name, tc.changed, "session", ""}}
						}
						fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: ordinary.conn, Statement: reader, SQL: metadataSQL}, ordinary.recorder,
							fixture.Select(metadataSQL, []string{"name", "setting", "source", "reset_val"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, want))
					}
					metadata(false)
				}
				// Prepare never mutates. Authority is re-evaluated at Execute.
				s.clientReads(tc, clientInitial(tc, s.pg), false)
				for _, grantee := range []string{"client_setting_login", "client_setting_grantee"} {
					if grantee == "client_setting_grantee" {
						s.run(fixture.Exec("GRANT client_setting_grantee TO client_setting_login", 0))
					}
					s.run(fixture.Exec("GRANT SET ON PARAMETER "+tc.name+" TO "+grantee, 0))
					fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: ordinary.conn, Statement: held, SQL: query}, ordinary.recorder, fixture.Exec(query, 0))
					metadata(false)
					if clientRestricted(tc.name) {
						ordinary.run(physicalError("SELECT current_setting("+physicalLiteral(tc.name)+")", physicalDenied(tc.name), false, true))
						if tc.name == "session_preload_libraries" {
							// Returning set_config's result needs read authority;
							// a SET grant alone must not install this candidate.
							ordinary.run(physicalError("SELECT set_config('session_preload_libraries','metadata_rejected',false)", physicalDenied(tc.name), true, true))
							s.run(fixture.Exec("GRANT pg_read_all_settings TO client_setting_login", 0))
							metadata(true)
							s.run(fixture.Exec("REVOKE pg_read_all_settings FROM client_setting_login", 0))
							metadata(false)
						}
					} else {
						ordinary.clientReads(tc, tc.changed, true)
					}
					s.run(fixture.Exec("REVOKE SET ON PARAMETER "+tc.name+" FROM "+grantee, 0))
					fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: ordinary.conn, Statement: held, SQL: query}, ordinary.recorder, physicalError(query, clientSetDenied(tc.name), false, false))
				}
				ordinary.run(physicalError("RESET "+tc.name, clientSetDenied(tc.name), true, false))
				ordinary.run(fixture.Exec("RESET ALL", 0))
				if clientRestricted(tc.name) {
					ordinary.run(physicalError("SHOW "+tc.name, physicalDenied(tc.name), false, true))
				} else {
					ordinary.clientReads(tc, clientInitial(tc, s.pg), false)
				}
				s.run(fixture.Exec("GRANT pg_read_all_settings TO client_setting_login", 0))
				ordinary.clientReads(tc, clientInitial(tc, s.pg), true)
				s.run(fixture.Exec("GRANT SET ON PARAMETER "+tc.name+" TO client_setting_login", 0))
				fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: ordinary.conn, Statement: held, SQL: query}, ordinary.recorder, fixture.Exec(query, 0))
				metadata(true)
				s.run(fixture.Exec("REVOKE SET ON PARAMETER "+tc.name+" FROM client_setting_login", 0))
				ordinary.clientReads(tc, tc.changed, true)
				fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: ordinary.conn, Statement: held, SQL: query}, ordinary.recorder, physicalError(query, clientSetDenied(tc.name), false, false))
				metadata(true)
				ordinary.run(fixture.Exec("BEGIN", 0))
				ordinary.run(fixture.Exec("RESET ALL", 0))
				ordinary.clientReads(tc, clientInitial(tc, s.pg), false)
				ordinary.run(fixture.Exec("ROLLBACK", 0))
				ordinary.clientReads(tc, tc.changed, false)
				ordinary.run(fixture.Exec("RESET ALL", 0))
				ordinary.clientReads(tc, clientInitial(tc, s.pg), false)
				ordinary.clientMetadata(tc, true)
				s.clientReads(tc, clientInitial(tc, s.pg), false)
				s.run(fixture.Exec("REVOKE pg_read_all_settings FROM client_setting_login", 0))
				metadata(false)
				if clientRestricted(tc.name) {
					ordinary.run(physicalError("SHOW "+tc.name, physicalDenied(tc.name), false, true))
				}
			})
		})
	}
	t.Run("tablespace-create-privilege", func(t *testing.T) {
		physicalCases(t, []string{"client_tablespace_login"}, func(s *physicalCase) {
			s.run(fixture.Exec("CREATE ROLE client_tablespace_login LOGIN PASSWORD 'role-core-fixture'", 0))
			ordinary := s.login("client_tablespace_login")
			ordinary.run(fixture.Exec("SET default_tablespace='pg_global'", 0))
			ordinary.run(fixture.Exec("SET temp_tablespaces='pg_default'", 0))
			ordinary.run(physicalError("SET temp_tablespaces='pg_global'", physicalDiagnostic("42501", "permission denied for tablespace pg_global"), true, false))
			s.run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_global TO client_tablespace_login", 0))
			ordinary.run(fixture.Exec("SET temp_tablespaces='pg_global'", 0))
			s.run(fixture.Exec("REVOKE CREATE ON TABLESPACE pg_global FROM client_tablespace_login", 0))
		})
	})
	// Host-registered privileged definitions remain deny-only, even for the
	// native bootstrap identity. PostgreSQL has no registration counterpart.
	db := sessionPool(t, settingsTestDriver(t), "client-deny-only")
	for _, name := range []string{"demo.priv"} {
		for _, query := range []string{"SET " + name + "='broken'", "SET " + name + "=DEFAULT", "RESET " + name} {
			_, err := db.ExecContext(context.Background(), query)
			fixture.AssertDiagnostic(t, err, func() *fixture.Diagnostic { d := clientSetDenied(name); return &d }())
		}
	}
}
