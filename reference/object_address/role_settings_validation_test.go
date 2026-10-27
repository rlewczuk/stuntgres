package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleSettingsValidationParameters(t *testing.T) {
	rsActionCases(t, []string{"rs_validation_login"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
		run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
		run(fixture.Exec("CREATE ROLE rs_validation_login", 0))
		run(fixture.Exec("ALTER ROLE rs_validation_login SET application_name='sentinel'", 0))
		for _, tc := range []struct{ tail, code, message, detail, hint string }{
			{"SET rs_unknown='x'", "42704", `unrecognized configuration parameter "rs_unknown"`, "", ""},
			{"SET rs_unknown FROM CURRENT", "42704", `unrecognized configuration parameter "rs_unknown"`, "", ""},
			{"SET work_mem='not_memory'", "22023", `invalid value for parameter "work_mem": "not_memory"`, "", ""},
			{"SET application_name TO 'a','b'", "22023", "SET application_name takes only one argument", "", ""},
			{"SET server_encoding='UTF8'", "55P02", `parameter "server_encoding" cannot be changed`, "", ""},
			{"SET max_connections=100", "55P02", `parameter "max_connections" cannot be changed without restarting the server`, "", ""},
			{"SET log_min_messages='warning'", "", "", "", ""},
			{"SET log_checkpoints=on", "55P02", `parameter "log_checkpoints" cannot be changed now`, "", ""},
			{"SET ignore_system_indexes=off", "55P02", `parameter "ignore_system_indexes" cannot be set after connection start`, "", ""},
			{"SET post_auth_delay=0", "55P02", `parameter "post_auth_delay" cannot be set after connection start`, "", ""},
		} {
			q := "ALTER ROLE rs_validation_login " + tc.tail
			if tc.code == "" {
				run(fixture.Exec(q, 0))
				run(fixture.Exec("ALTER ROLE rs_validation_login RESET log_min_messages", 0))
				continue
			}
			s := rsActionError(q, tc.code, tc.message)
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Detail = tc.detail
			d.Hint = tc.hint
			s = fixture.ImplicitError(q, d)
			s.ErrorStage = "execute"
			run(s)
			run(rsActionConfig("rs_validation_login", "{application_name=sentinel}"))
		}
		run(rsActionError("ALTER ROLE rs_validation_missing SET application_name='x'", "42704", `role "rs_validation_missing" does not exist`))
	})
}

func TestReferenceRoleSettingsValidationUnknownResetRow(t *testing.T) {
	rsActionCases(t, []string{"rs_reset_login"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
		run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
		run(fixture.Exec("CREATE ROLE rs_reset_login", 0))
		// AlterSetting validates deletion only when there is an exact catalog row.
		for _, tail := range []string{"RESET rs_unknown", "SET rs_unknown TO DEFAULT"} {
			run(fixture.Exec("ALTER ROLE rs_reset_login "+tail, 0))
			run(rsActionConfig("rs_reset_login", ""))
		}
		run(fixture.Exec("ALTER ROLE rs_reset_login SET application_name='keep'", 0))
		for _, tail := range []string{"RESET rs_unknown", "SET rs_unknown TO DEFAULT"} {
			run(rsActionError("ALTER ROLE rs_reset_login "+tail, "42704", `unrecognized configuration parameter "rs_unknown"`))
			run(rsActionConfig("rs_reset_login", "{application_name=keep}"))
		}
		run(fixture.Exec("ALTER ROLE rs_reset_login RESET ALL", 0))
		run(fixture.Exec("ALTER ROLE rs_reset_login RESET rs_unknown", 0))
	})
}

func TestReferenceRoleSettingsValidationMalformedSuffix(t *testing.T) {
	rsActionCases(t, []string{"rs_suffix_login"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
		run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
		run(fixture.Exec("CREATE ROLE rs_suffix_login", 0))
		run(fixture.Exec("ALTER ROLE rs_suffix_login SET application_name='sentinel'", 0))
		for _, prefix := range []string{
			"ALTER ROLE rs_suffix_login SET application_name TO 'changed'",
			"ALTER USER rs_suffix_login SET application_name = 'changed'",
			"ALTER ROLE rs_suffix_login SET application_name FROM CURRENT",
			"ALTER ROLE rs_suffix_login SET application_name=DEFAULT",
			"ALTER USER rs_suffix_login RESET application_name",
			"ALTER ROLE rs_suffix_login RESET ALL",
		} {
			q := prefix + " unexpected_suffix"
			d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "unexpected_suffix"`)
			d.Position = fixture.Position(strings.Index(q, "unexpected_suffix") + 1)
			s := fixture.ImplicitError(q, d)
			if route == "direct" {
				s.ErrorStage = "execute"
			} else {
				s.ErrorStage = "prepare"
			}
			run(s)
			run(rsActionConfig("rs_suffix_login", "{application_name=sentinel}"))
		}
	})
}

func TestReferenceRoleSettingsValidationContextResets(t *testing.T) {
	rsActionCases(t, []string{"rs_context_login"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
		run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
		run(fixture.Exec("CREATE ROLE rs_context_login", 0))
		run(fixture.Exec("ALTER ROLE rs_context_login SET application_name='sentinel'", 0))
		for _, tc := range []struct{ name, message string }{
			{"server_encoding", `parameter "server_encoding" cannot be changed`},
			{"max_connections", `parameter "max_connections" cannot be changed without restarting the server`},
			{"log_checkpoints", `parameter "log_checkpoints" cannot be changed now`},
			{"ignore_system_indexes", `parameter "ignore_system_indexes" cannot be set after connection start`},
			{"post_auth_delay", `parameter "post_auth_delay" cannot be set after connection start`},
		} {
			for _, tail := range []string{"RESET " + tc.name, "SET " + tc.name + "=DEFAULT", "SET " + tc.name + " FROM CURRENT"} {
				run(rsActionError("ALTER ROLE rs_context_login "+tail, "55P02", tc.message))
				run(rsActionConfig("rs_context_login", "{application_name=sentinel}"))
			}
		}
	})
}
