//go:build !noservertests

package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceServerSimpleExecuteTiming(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			peer := serverAuthenticated(t, dsn)
			serverWantCode(t, peer.query(t, "CREATE TABLE server_execute_timing(v integer); INSERT INTO server_execute_timing VALUES (0)"), "")
			for _, c := range []struct {
				name, prepare, sql, code, message string
			}{
				{
					name:    "row_error_is_store_before_output",
					prepare: "PREPARE server_simple_execute_row AS SELECT 10/v FROM server_execute_timing",
					sql:     "EXECUTE server_simple_execute_row",
					code:    "22012", message: "division by zero",
				},
				{
					name:    "inner_planning_error_after_outer_portal_creation",
					prepare: "PREPARE server_simple_execute_constant AS SELECT 1/0",
					sql:     "EXECUTE server_simple_execute_constant",
					code:    "22012", message: "division by zero",
				},
				{
					name: "inner_missing_name_after_outer_portal_creation",
					sql:  "EXECUTE server_simple_execute_missing",
					code: "26000", message: "prepared statement \"server_simple_execute_missing\" does not exist",
				},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
					if c.prepare != "" {
						serverWantCode(t, w.query(t, c.prepare), "")
					}
					serverWantCode(t, w.query(t, "BEGIN"), "")
					frames := serverCycle(t, w,
						serverParse("rows", "SELECT v FROM (VALUES (1),(2),(3)) AS q(v) ORDER BY v"),
						serverBind("", "rows", nil, nil), serverBind("named", "rows", nil, nil),
						serverExecute("", 1), serverExecute("named", 1))
					serverWantTags(t, frames, "122DsDsZ")
					serverWantCode(t, frames, "")
					serverWantValues(t, frames, [][]string{{"1"}, {"1"}})
					serverWantReady(t, frames, 'T')
					// Named extended controls cannot themselves replace the unnamed
					// portal whose utility-creation timing this case distinguishes.
					serverReviewMark(t, w)
					frames = w.query(t, c.sql)
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, c.code, c.message, "")
					serverWantReady(t, frames, 'E')
					serverWantCompletions(t, frames)
					serverReviewRestore(t, w)
					frames = serverCycle(t, w, serverExecute("", 0))
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, "34000", "portal \"\" does not exist", "")
					serverWantReady(t, frames, 'E')
					serverReviewRestore(t, w)
					frames = serverCycle(t, w, serverExecute("named", 1))
					serverWantTags(t, frames, "DsZ")
					serverWantValues(t, frames, [][]string{{"2"}})
					serverWantReady(t, frames, 'T')
					frames = serverCycle(t, w, serverExecute("named", 0))
					serverWantTags(t, frames, "DCZ")
					serverWantValues(t, frames, [][]string{{"3"}})
					serverWantCompletions(t, frames, "SELECT 1")
					frames = w.query(t, "ROLLBACK")
					serverWantTags(t, frames, "CZ")
					serverWantCompletions(t, frames, "ROLLBACK")
					serverWantReady(t, frames, 'I')
					serverScalar(t, w, "SELECT 42", "42")
					serverScalar(t, peer, "SELECT v FROM server_execute_timing", "0")
				})
			}
		})
	}
}
