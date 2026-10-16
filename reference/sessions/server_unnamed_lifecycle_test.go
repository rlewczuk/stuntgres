//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerFailedUnnamedParse(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, c := range []struct{ name, sql, code, message, position string }{
				{"syntax", "SELECT )", "42601", "syntax error at or near \")\"", "8"},
				{"multiple_commands", "SELECT 1; SELECT 2", "42601", "cannot insert multiple commands into a prepared statement", ""},
				{"semantic", "SELECT missing", "42703", "column \"missing\" does not exist", "8"},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, f.DSNFor(b))
					defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
					serverWantCode(t, w.query(t, "BEGIN"), "")
					serverWantTags(t, serverCycle(t, w, serverParse("", "SELECT 42"), serverBind("retained", "", nil, nil)), "12Z")
					serverReviewMark(t, w)
					frames := serverCycle(t, w, serverParse("", c.sql))
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, c.code, c.message, c.position)
					serverWantReady(t, frames, 'E')
					serverReviewRestore(t, w)
					frames = serverCycle(t, w, serverBind("new", "", nil, nil))
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, "26000", "unnamed prepared statement does not exist", "")
					serverReviewRestore(t, w)
					// The old registry slot is gone, but the portal owns its initialized body.
					frames = serverCycle(t, w, serverExecute("retained", 0))
					serverWantTags(t, frames, "DCZ")
					serverWantValues(t, frames, [][]string{{"42"}})
					serverWantCompletions(t, frames, "SELECT 1")
				})
			}
		})
	}
}

func TestReferenceServerUnnamedPortalQueryLifecycle(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			setup := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, setup.query(t, "CREATE TABLE server_unnamed_fault(v integer); INSERT INTO server_unnamed_fault VALUES (0)"), "")
			for _, completed := range []bool{false, true} {
				sequence := "server_unnamed_prefix_suspended"
				if completed {
					sequence = "server_unnamed_prefix_completed"
				}
				for _, c := range []struct {
					name, sql, tags, code, message, position string
					survives                                 bool
				}{
					{"valid", "SELECT 42", "TDCZ", "", "", "", false},
					{"empty", "", "IZ", "", "", "", true},
					{"syntax_failure", "SELECT )", "EZ", "42601", "syntax error at or near \")\"", "8", true},
					{"analysis_failure", "SELECT missing", "EZ", "42703", "column \"missing\" does not exist", "8", true},
					{"planning_failure", "SELECT 1/0", "EZ", "22012", "division by zero", "", true},
					{"execution_failure", "SELECT 10/v FROM server_unnamed_fault", "TEZ", "22012", "division by zero", "", false},
					{"execution_after_row", "SELECT 10 / (2 - nextval('" + sequence + "')) FROM (VALUES (1),(1)) AS q(v)", "TDEZ", "22012", "division by zero", "", false},
				} {
					name := c.name
					if completed {
						name = "completed/" + name
					} else {
						name = "suspended/" + name
					}
					t.Run(name, func(t *testing.T) {
						w := serverAuthenticated(t, f.DSNFor(b))
						partial := c.name == "execution_after_row"
						if partial {
							// Committed setup precedes BEGIN and the unnamed Bind.
							// Identical source rows impose no unordered-row assumption.
							serverWantCode(t, setup.query(t, "CREATE SEQUENCE "+sequence+" START 1"), "")
							defer func() { serverWantCode(t, setup.query(t, "DROP SEQUENCE "+sequence), "") }()
							serverScalar(t, setup, "SELECT is_called FROM "+sequence, "f")
						}
						defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
						serverWantCode(t, w.query(t, "BEGIN"), "")
						frames := serverCycle(t, w, serverParse("rows", "SELECT v FROM (VALUES (1),(2)) AS q(v) ORDER BY v"), serverBind("", "rows", nil, nil), serverBind("named", "rows", nil, nil))
						serverWantTags(t, frames, "122Z")
						limit := uint32(1)
						if completed {
							limit = 0
						}
						frames = serverCycle(t, w, serverExecute("", limit))
						if completed {
							serverWantTags(t, frames, "DDCZ")
							serverWantValues(t, frames, [][]string{{"1"}, {"2"}})
						} else {
							serverWantTags(t, frames, "DsZ")
							serverWantValues(t, frames, [][]string{{"1"}})
						}
						serverReviewMark(t, w)
						frames = w.query(t, c.sql)
						serverWantTags(t, frames, c.tags)
						if c.code != "" {
							serverNotificationError(t, frames, c.code, c.message, c.position)
							serverWantReady(t, frames, 'E')
							if partial {
								serverWantValues(t, frames, [][]string{{"10"}})
								serverWantCompletions(t, frames)
								serverScalar(t, setup, "SELECT last_value FROM "+sequence, "2")
							}
							serverReviewRestore(t, w)
						} else {
							serverWantCode(t, frames, "")
							serverWantReady(t, frames, 'T')
							if c.name == "valid" {
								serverWantValues(t, frames, [][]string{{"42"}})
							}
						}
						frames = serverCycle(t, w, serverExecute("", 1))
						if c.survives {
							if completed {
								serverWantTags(t, frames, "CZ")
								serverWantCompletions(t, frames, "SELECT 0")
							} else {
								serverWantTags(t, frames, "DsZ")
								serverWantValues(t, frames, [][]string{{"2"}})
							}
						} else {
							serverWantTags(t, frames, "EZ")
							serverNotificationError(t, frames, "34000", "portal \"\" does not exist", "")
							serverReviewRestore(t, w)
						}
						// A valid simple Query replaced only the unnamed portal, never its sibling.
						frames = serverCycle(t, w, serverExecute("named", 0))
						serverWantTags(t, frames, "DDCZ")
						serverWantValues(t, frames, [][]string{{"1"}, {"2"}})
						if partial {
							serverScalar(t, setup, "SELECT last_value FROM "+sequence, "2")
						}
					})
				}
			}
		})
	}
}

// Simple Q for SAVEPOINT/ROLLBACK would itself invalidate the unnamed objects
// under test. Named extended control portals avoid contaminating the witness.
func serverReviewMark(t *testing.T, w *serverWire) {
	t.Helper()
	frames := serverCycle(t, w, serverParse("review_mark", "SAVEPOINT mark"), serverBind("review_mark", "review_mark", nil, nil), serverExecute("review_mark", 0), serverClose('P', "review_mark"), serverParse("review_restore", "ROLLBACK TO mark"))
	serverWantTags(t, frames, "12C31Z")
	serverWantReady(t, frames, 'T')
}
func serverReviewRestore(t *testing.T, w *serverWire) {
	t.Helper()
	frames := serverCycle(t, w, serverBind("review_restore", "review_restore", nil, nil), serverExecute("review_restore", 0), serverClose('P', "review_restore"))
	serverWantTags(t, frames, "2C3Z")
	serverWantReady(t, frames, 'T')
}
