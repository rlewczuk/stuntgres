package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLegacySnapshotDiagnostics(t *testing.T) {
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct{ call, signature string }{
		{"txid_current(1)", "txid_current(integer)"},
		{"txid_current_if_assigned(1)", "txid_current_if_assigned(integer)"},
		{"txid_current_snapshot(1)", "txid_current_snapshot(integer)"},
		{"txid_snapshot_xip()", "txid_snapshot_xip()"},
		{"txid_snapshot_xmin('10:20:','10:20:')", "txid_snapshot_xmin(unknown, unknown)"},
		{"txid_snapshot_xmax()", "txid_snapshot_xmax()"},
		{"txid_visible_in_snapshot(11)", "txid_visible_in_snapshot(integer)"},
		{"txid_status()", "txid_status()"},
		{"txid_snapshot_xmin(1)", "txid_snapshot_xmin(integer)"},
		{"txid_snapshot_xmax(true)", "txid_snapshot_xmax(boolean)"},
		{"txid_snapshot_xip('10:20:'::text)", "txid_snapshot_xip(text)"},
		{"txid_status(true)", "txid_status(boolean)"},
		{"txid_status('11'::text)", "txid_status(text)"},
		{"txid_status('11'::xid8)", "txid_status(xid8)"},
		{"txid_visible_in_snapshot(true,'10:20:')", "txid_visible_in_snapshot(boolean, unknown)"},
		{"txid_visible_in_snapshot('11'::text,'10:20:')", "txid_visible_in_snapshot(text, unknown)"},
		{"txid_visible_in_snapshot('11'::xid8,'10:20:')", "txid_visible_in_snapshot(xid8, unknown)"},
		{"txid_visible_in_snapshot(11,'10:20:'::text)", "txid_visible_in_snapshot(integer, text)"},
		{"txid_snapshot_xmin('10:20:'::pg_snapshot)", "txid_snapshot_xmin(pg_snapshot)"},
		{"txid_snapshot_xmax('10:20:'::pg_snapshot)", "txid_snapshot_xmax(pg_snapshot)"},
		{"txid_snapshot_xip('10:20:'::pg_snapshot)", "txid_snapshot_xip(pg_snapshot)"},
		{"txid_visible_in_snapshot(11,'10:20:'::pg_snapshot)", "txid_visible_in_snapshot(integer, pg_snapshot)"},
		{"pg_snapshot_xmin('10:20:'::txid_snapshot)", "pg_snapshot_xmin(txid_snapshot)"},
		{"pg_snapshot_xmax('10:20:'::txid_snapshot)", "pg_snapshot_xmax(txid_snapshot)"},
		{"pg_snapshot_xip('10:20:'::txid_snapshot)", "pg_snapshot_xip(txid_snapshot)"},
		{"pg_visible_in_snapshot('11'::xid8,'10:20:'::txid_snapshot)", "pg_visible_in_snapshot(xid8, txid_snapshot)"},
		{"txid_snapsho_xmin('10:20:')", "txid_snapsho_xmin(unknown)"},
		{"\"TXID_SNAPSHOT_XMIN\"('10:20:')", "TXID_SNAPSHOT_XMIN(unknown)"},
		{"txid_snapshot_xmin(s => '10:20:')", "txid_snapshot_xmin(s => unknown)"},
		{"txid_status(id => 1)", "txid_status(id => integer)"},
		{"txid_snapshot_xmin(VARIADIC ARRAY['10:20:']::text[])", "txid_snapshot_xmin(text[])"},
	} {
		t.Run(tc.call, func(t *testing.T) {
			legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				identityError(t, p, b, snapshotConn(t, p, b), "SELECT "+tc.call, fixture.Diagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: hint, Position: "8"}, "prepare")
			})
		})
	}
	for _, tc := range []struct{ name, query, code, message, hint, position string }{
		{"unicode", "/*é*/ SELECT txid_snapshot_xmin(1)", "42883", "function txid_snapshot_xmin(integer) does not exist", hint, "14"},
		{"schema", "SELECT legacy_absent.txid_snapshot_xmin('10:20:')", "3F000", `schema "legacy_absent" does not exist`, "", "8"},
		{"star", "SELECT txid_snapshot_xmin(*)", "42883", "function txid_snapshot_xmin() does not exist", hint, "8"},
		{"distinct", "SELECT txid_snapshot_xmin(DISTINCT '10:20:')", "42809", "DISTINCT specified, but txid_snapshot_xmin is not an aggregate function", "", "8"},
		{"order", "SELECT txid_snapshot_xmin('10:20:' ORDER BY 1)", "42809", "ORDER BY specified, but txid_snapshot_xmin is not an aggregate function", "", "8"},
		{"filter", "SELECT txid_snapshot_xmin('10:20:') FILTER (WHERE true)", "42809", "FILTER specified, but txid_snapshot_xmin is not an aggregate function", "", "8"},
		{"over", "SELECT txid_snapshot_xmin('10:20:') OVER ()", "42809", "OVER specified, but txid_snapshot_xmin is not a window function nor an aggregate function", "", "8"},
		{"integer-cast", "SELECT CAST(1 AS txid_snapshot)", "42846", "cannot cast type integer to txid_snapshot", "", "8"},
		{"legacy-to-modern", "SELECT CAST('10:20:'::txid_snapshot AS pg_snapshot)", "42846", "cannot cast type txid_snapshot to pg_snapshot", "", "8"},
		{"modern-to-legacy", "SELECT CAST('10:20:'::pg_snapshot AS txid_snapshot)", "42846", "cannot cast type pg_snapshot to txid_snapshot", "", "8"},
		{"modifier", "SELECT '10:20:'::txid_snapshot(2)", "42601", `type modifier is not allowed for type "txid_snapshot"`, "", "18"},
		{"unicode-literal", "/*é*/ SELECT 'bad'::txid_snapshot", "22P02", `invalid input syntax for type pg_snapshot: "bad"`, "", "14"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				identityError(t, p, b, snapshotConn(t, p, b), tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.position}, "prepare")
			})
		})
	}
	t.Run("ordinary-coercions", func(t *testing.T) {
		legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			identityBoth(t, p, b, b.DB, legacySnapshotStep(b, "SELECT TXID_SNAPSHOT_XMIN('10:20:') AS a,pg_catalog.txid_snapshot_xmax('10:20:') AS b,CAST('12:16:14,14'::text AS txid_snapshot) AS c", []string{"a", "b", "c"}, []string{"INT8", "INT8", "TXID_SNAPSHOT"}, [][]any{{int64(10), int64(20), "12:16:14"}}))
			for _, arg := range []string{"11", "11::smallint", "11::integer", "11::bigint"} {
				identityBoth(t, p, b, b.DB, fixture.Select("SELECT txid_visible_in_snapshot("+arg+",'10:20:') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
			}
			for _, arg := range []string{"1", "1::smallint", "1::integer", "1::bigint"} {
				identityBoth(t, p, b, b.DB, fixture.Select("SELECT txid_status("+arg+") AS s", []string{"s"}, []string{"TEXT"}, [][]any{{"committed"}}))
			}
		})
	})
}
