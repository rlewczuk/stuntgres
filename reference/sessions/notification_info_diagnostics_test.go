package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceNotificationInfoDiagnostics(t *testing.T) {
	sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn := notificationInfoConn(t, p, b)
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		type sample struct {
			query, code, message, hint string
			position                   int
		}
		for _, name := range []string{"pg_listening_channels", "pg_notification_queue_usage"} {
			cases := []sample{
				{"SELECT " + name + "(1)", "42883", "function " + name + "(integer) does not exist", hint, 8},
				{"SELECT " + name + "(NULL)", "42883", "function " + name + "(unknown) does not exist", hint, 8},
				{"SELECT " + name + "('x'::text)", "42883", "function " + name + "(text) does not exist", hint, 8},
				{"SELECT " + name + "(NULL::text)", "42883", "function " + name + "(text) does not exist", hint, 8},
				{"SELECT " + name + "(1,2)", "42883", "function " + name + "(integer, integer) does not exist", hint, 8},
				{`SELECT "` + strings.ToUpper(name) + `"()`, "42883", "function " + strings.ToUpper(name) + "() does not exist", hint, 8},
				{"SELECT public." + name + "()", "42883", "function public." + name + "() does not exist", hint, 8},
				{"SELECT missing_notification_info." + name + "()", "3F000", `schema "missing_notification_info" does not exist`, "", 8},
				{"/*é🙂*/ SELECT " + name + "(NULL)", "42883", "function " + name + "(unknown) does not exist", hint, 15},
				{"SELECT " + name + "(missing_notification_argument)", "42703", `column "missing_notification_argument" does not exist`, "", 9 + len(name)},
				{"SELECT " + name + "(*)", "42809", name + "(*) specified, but " + name + " is not an aggregate function", "", 8},
				{"SELECT " + name + "() OVER ()", "42809", "OVER specified, but " + name + " is not a window function nor an aggregate function", "", 8},
			}
			if name == "pg_listening_channels" {
				const nested = "SELECT * FROM generate_series(1,pg_listening_channels()::integer)"
				cases = append(cases,
					sample{"SELECT 1 WHERE pg_listening_channels() IS NULL", "0A000", "set-returning functions are not allowed in WHERE", "", 16},
					sample{nested, "0A000", "set-returning functions must appear at top level of FROM", "", strings.Index(nested, name) + 1},
				)
			}
			for _, tc := range cases {
				t.Run(tc.query, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(tc.query, fixture.Diagnostic{
							Code: tc.code, Message: tc.message, Hint: tc.hint, Position: fixture.Position(tc.position),
						})
						s.SelectRows, s.Prepared = true, prepared
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
					}
				})
			}
		}
		// A statement error aborts the existing transaction, not its physical owner.
		sessionMessageSQL(t, p, b, conn, "BEGIN")
		failure := fixture.ImplicitError("SELECT pg_notification_queue_usage(1)", fixture.Diagnostic{
			Code: "42883", Message: "function pg_notification_queue_usage(integer) does not exist", Hint: hint, Position: "8",
		})
		failure.SelectRows = true
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, failure)
		aborted := fixture.ErrorDiagnostic(notificationInfoUsageSQL, fixture.TransactionAborted())
		aborted.SelectRows = true
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, aborted)
		sessionMessageSQL(t, p, b, conn, "ROLLBACK")
		notificationInfoWantUsage(t, p, conn, 0)
		sessionMessageSQL(t, p, b, conn, "LISTEN diagnostics_survivor")
		notificationInfoChannels(t, p, b, conn, "SELECT pg_listening_channels()", "diagnostics_survivor")
	})
}
