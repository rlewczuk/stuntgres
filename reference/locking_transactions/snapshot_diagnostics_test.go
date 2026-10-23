package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotDiagnostics(t *testing.T) {
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct{ call, signature string }{
		{"pg_current_snapshot(1)", "pg_current_snapshot(integer)"},
		{"pg_snapshot_xip()", "pg_snapshot_xip()"},
		{"pg_snapshot_xmin('10:20:'::pg_snapshot,'10:20:'::pg_snapshot)", "pg_snapshot_xmin(pg_snapshot, pg_snapshot)"},
		{"pg_snapshot_xmax()", "pg_snapshot_xmax()"},
		{"pg_visible_in_snapshot('10:20:'::pg_snapshot)", "pg_visible_in_snapshot(pg_snapshot)"},
		{"pg_snapshot_xmin(1)", "pg_snapshot_xmin(integer)"},
		{"pg_snapshot_xmax(true)", "pg_snapshot_xmax(boolean)"},
		{"pg_snapshot_xip('10:20:'::text)", "pg_snapshot_xip(text)"},
		{"pg_visible_in_snapshot(1,'10:20:')", "pg_visible_in_snapshot(integer, unknown)"},
		{"pg_visible_in_snapshot('11'::xid8,'10:20:'::text)", "pg_visible_in_snapshot(xid8, text)"},
		{"pg_snapsho_xmin('10:20:')", "pg_snapsho_xmin(unknown)"},
		{"\"PG_SNAPSHOT_XMIN\"('10:20:')", "PG_SNAPSHOT_XMIN(unknown)"},
		{"pg_snapshot_xmin(s => '10:20:')", "pg_snapshot_xmin(s => unknown)"},
		{"pg_snapshot_xmin(VARIADIC ARRAY['10:20:']::text[])", "pg_snapshot_xmin(text[])"},
	} {
		t.Run(tc.call, func(t *testing.T) {
			snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := snapshotConn(t, p, b)
				identityError(t, p, b, c, "SELECT "+tc.call, fixture.Diagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: hint, Position: "8"}, "prepare")
			})
		})
	}
	for _, tc := range []struct{ name, query, code, message, hint, position string }{
		{"unicode", "/*é*/ SELECT pg_snapshot_xmin(1)", "42883", "function pg_snapshot_xmin(integer) does not exist", hint, "14"},
		{"schema", "SELECT snapshot_absent.pg_snapshot_xmin('10:20:')", "3F000", `schema "snapshot_absent" does not exist`, "", "8"},
		{"star", "SELECT pg_snapshot_xmin(*)", "42883", "function pg_snapshot_xmin() does not exist", hint, "8"},
		{"distinct", "SELECT pg_snapshot_xmin(DISTINCT '10:20:')", "42809", "DISTINCT specified, but pg_snapshot_xmin is not an aggregate function", "", "8"},
		{"order", "SELECT pg_snapshot_xmin('10:20:' ORDER BY 1)", "42809", "ORDER BY specified, but pg_snapshot_xmin is not an aggregate function", "", "8"},
		{"filter", "SELECT pg_snapshot_xmin('10:20:') FILTER (WHERE true)", "42809", "FILTER specified, but pg_snapshot_xmin is not an aggregate function", "", "8"},
		{"over", "SELECT pg_snapshot_xmin('10:20:') OVER ()", "42809", "OVER specified, but pg_snapshot_xmin is not a window function nor an aggregate function", "", "8"},
		{"integer-cast", "SELECT CAST(1 AS pg_snapshot)", "42846", "cannot cast type integer to pg_snapshot", "", "8"},
		{"modifier", "SELECT '10:20:'::pg_snapshot(2)", "42601", `type modifier is not allowed for type "pg_snapshot"`, "", "18"},
		{"unicode-literal", "/*é*/ SELECT 'bad'::pg_snapshot", "22P02", `invalid input syntax for type pg_snapshot: "bad"`, "", "14"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				identityError(t, p, b, snapshotConn(t, p, b), tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.position}, "prepare")
			})
		})
	}
	t.Run("qualified-and-text-cast", func(t *testing.T) {
		snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			snapshotBoth(t, p, b, b.DB, fixture.Select("SELECT PG_SNAPSHOT_XMIN('10:20:')::text AS a,pg_catalog.pg_snapshot_xmax('10:20:')::text AS b,CAST('12:16:14,14'::text AS pg_snapshot)::text AS c", []string{"a", "b", "c"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"10", "20", "12:16:14"}}))
		})
	})
}
