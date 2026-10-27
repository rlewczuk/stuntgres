package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSettingsCallOutputLabelsDiagnostics(t *testing.T) {
	cases := []struct {
		name, query string
		args        []any
		diagnostic  fixture.Diagnostic
		want        any
	}{
		{"missing", `SELECT current_setting($1) AS value`, []any{"label_missing"}, fixture.Diagnostic{Code: "42704", Message: `unrecognized configuration parameter "label_missing"`}, nil},
		{"missing-false", `SELECT pg_catalog.current_setting($1,$2) value`, []any{"label_missing", false}, fixture.Diagnostic{Code: "42704", Message: `unrecognized configuration parameter "label_missing"`}, nil},
		{"missing-true", `SELECT pg_catalog.current_setting($1,$2) value`, []any{"label_missing", true}, fixture.Diagnostic{}, nil},
		{"null-name", `SELECT current_setting($1) AS value`, []any{nil}, fixture.Diagnostic{}, nil},
		{"null-name-optional", `SELECT current_setting($1,$2) value`, []any{nil, true}, fixture.Diagnostic{}, nil},
		{"null-flag", `SELECT pg_catalog.current_setting($1,$2) value`, []any{"application_name", nil}, fixture.Diagnostic{}, nil},
		{"write-null-name", `SELECT pg_catalog.set_config($1,$2,$3) AS value`, []any{nil, "x", false}, fixture.Diagnostic{Code: "22004", Message: "SET requires parameter name"}, nil},
		{"invalid-enum", `SELECT pg_catalog.set_config($1,$2,$3) AS value`, []any{"client_min_messages", "bogus", false}, fixture.Diagnostic{Code: "22023", Message: `invalid value for parameter "client_min_messages": "bogus"`, Hint: "Available values: debug5, debug4, debug3, debug2, debug1, log, notice, warning, error."}, nil},
		{"immutable", `SELECT pg_catalog.set_config($1,$2,$3) AS value`, []any{"server_encoding", "UTF8", false}, fixture.Diagnostic{Code: "55P02", Message: `parameter "server_encoding" cannot be changed`}, nil},
		{"boolean-write", `SELECT set_config($1,$2,$3) AS value`, []any{"application_name", "lost", "maybe"}, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "maybe"`, Context: "unnamed portal parameter $3 = '...'"}, nil},
		{"boolean-read", `SELECT pg_catalog.current_setting($1,$2) value`, []any{"application_name", "maybe"}, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "maybe"`, Context: "unnamed portal parameter $2 = '...'"}, nil},
	}
	for _, tc := range cases {
		for _, route := range []string{"direct", "fresh", "held"} {
			t.Run(tc.name+"/"+route, func(t *testing.T) {
				settingsLabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, peer *sql.Conn) {
					fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("SET application_name='label-sentinel'", 0))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("SET application_name='label-peer'", 0))
					run := settingsLabelRoute(t, p.Ctx, owner, b.Recorder, route, tc.query)
					if tc.diagnostic.Code == "" {
						run(settingsLabelValue(b.PG, tc.query, "value", tc.want, tc.args...))
					} else {
						run(settingsLabelFailure(tc.query, tc.diagnostic, tc.args...))
					}
					settingsLabelState(t, p, b, owner, peer, "label-sentinel", "label-peer")
				})
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("literal-boolean/", prepared), func(t *testing.T) {
			settingsLabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, peer *sql.Conn) {
				fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("SET application_name='label-sentinel'", 0))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("SET application_name='label-peer'", 0))
				s := settingsLabelFailure(`SELECT set_config('application_name','ok','maybe') AS value`, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "maybe"`, Position: "43"})
				s.Prepared = prepared
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, owner, b.Recorder, s)
				settingsLabelState(t, p, b, owner, peer, "label-sentinel", "label-peer")
			})
		})
	}
	for _, route := range []string{"direct", "fresh", "held"} {
		t.Run("authority/"+route, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"label_call_reader"})
			settingsLabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, peer *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, b.Recorder, s) }
				run(fixture.Exec("CREATE ROLE label_call_reader", 0))
				run(fixture.Exec("SET application_name='label-sentinel'", 0))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("SET application_name='label-peer'", 0))
				targets := []string{"pg_catalog.current_setting(text)", "pg_catalog.current_setting(text,boolean)", "pg_catalog.set_config(text,text,boolean)"}
				// Builtin ACLs are not restored by public-schema reset. This defer
				// runs on Fatal too, before returning the physical owner.
				defer func() {
					if _, err := owner.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
						t.Error(err)
						return
					}
					for _, target := range targets {
						for _, sql := range []string{"GRANT EXECUTE ON FUNCTION " + target + " TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + target + " FROM label_call_reader CASCADE"} {
							if _, err := owner.ExecContext(p.Ctx, sql); err != nil {
								t.Error("builtin ACL cleanup", err)
							}
						}
					}
				}()
				for _, flag := range []bool{false, true} {
					query := `SELECT pg_catalog.current_setting($1,$2) AS value`
					execute := settingsLabelRoute(t, p.Ctx, owner, b.Recorder, route, query)
					fixture.RoleGrantActor(run, "label_call_reader", func() {
						execute(settingsLabelFailure(query, fixture.Diagnostic{Code: "42501", Message: `permission denied to examine "config_file"`, Detail: `Only roles with privileges of the "pg_read_all_settings" role may examine this parameter.`}, "config_file", flag))
					})
					settingsLabelState(t, p, b, owner, peer, "label-sentinel", "label-peer")
				}
				query := `SELECT pg_catalog.set_config($1,$2,$3) value`
				execute := settingsLabelRoute(t, p.Ctx, owner, b.Recorder, route, query)
				fixture.RoleGrantActor(run, "label_call_reader", func() {
					execute(settingsLabelFailure(query, fixture.Diagnostic{Code: "42501", Message: `permission denied to set parameter "log_statement_stats"`}, "log_statement_stats", "on", false))
				})
				settingsLabelState(t, p, b, owner, peer, "label-sentinel", "label-peer")
				for i, tc := range []struct {
					query, function string
					args            []any
				}{
					{`SELECT pg_catalog.current_setting($1) AS value`, "current_setting", []any{"application_name"}},
					{`SELECT pg_catalog.current_setting($1,$2) value`, "current_setting", []any{"application_name", false}},
					{`SELECT pg_catalog.set_config($1,$2,$3) AS value`, "set_config", []any{"application_name", "label-sentinel", false}},
				} {
					// Prepare and warm the SAME non-NULL handle as the ordinary
					// actor before revoke; later phases do not re-Prepare it.
					var held fixture.HeldStatement
					fixture.RoleGrantActor(run, "label_call_reader", func() {
						stmt, err := owner.PrepareContext(p.Ctx, tc.query)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { stmt.Close() })
						held = fixture.HeldStatement{Executor: owner, Statement: stmt, SQL: tc.query}
						fixture.RunStep(t, p.Ctx, held, b.Recorder, settingsLabelValue(b.PG, tc.query, "value", "label-sentinel", tc.args...))
					})
					var current func(fixture.Step)
					if route != "held" {
						current = settingsLabelRoute(t, p.Ctx, owner, b.Recorder, route, tc.query)
					}
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+targets[i]+" FROM PUBLIC", 0))
					for _, allowed := range []bool{false, true} {
						if allowed {
							run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+targets[i]+" TO label_call_reader", 0))
						}
						fixture.RoleGrantActor(run, "label_call_reader", func() {
							s := settingsLabelValue(b.PG, tc.query, "value", "label-sentinel", tc.args...)
							if !allowed {
								s = settingsLabelFailure(tc.query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + tc.function}, tc.args...)
							}
							if current != nil {
								current(s)
							}
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						})
						settingsLabelState(t, p, b, owner, peer, "label-sentinel", "label-peer")
					}
					// Restore this overload before testing its independent sibling.
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+targets[i]+" TO PUBLIC", 0))
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+targets[i]+" FROM label_call_reader", 0))
				}
			})
		})
	}
}
