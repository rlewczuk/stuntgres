package reference_test

import (
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLegacySnapshotConsumers(t *testing.T) {
	legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := snapshotConn(t, p, b)
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE legacy_snapshots (id integer,s txid_snapshot DEFAULT '12:16:14,14'::txid_snapshot)", 0),
			fixture.Exec("INSERT INTO legacy_snapshots (id) VALUES (1)", 1),
			fixture.Exec("INSERT INTO legacy_snapshots (id,s) VALUES (2,NULL)", 1),
			fixture.Exec("CREATE VIEW legacy_snapshot_literal AS SELECT '12:16:14,14'::txid_snapshot AS s", 0),
			fixture.Exec("CREATE VIEW legacy_snapshot_live AS SELECT txid_current_snapshot() AS s", 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		identityBoth(t, p, b, c, fixture.Exec("INSERT INTO legacy_snapshots (id,s) VALUES ($1,$2)", 1, int64(3), []byte("12:20:13,15,18")))
		identityBoth(t, p, b, c, fixture.Select("SELECT id,s::text AS s,pg_typeof(s)::text AS kind FROM legacy_snapshots ORDER BY id", []string{"id", "s", "kind"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "12:16:14", "txid_snapshot"}, {int64(2), nil, "txid_snapshot"}, {int64(3), "12:20:13,15,18", "txid_snapshot"}, {int64(3), "12:20:13,15,18", "txid_snapshot"}}))
		identityBoth(t, p, b, c, legacySnapshotStoredStep(b, "SELECT s FROM legacy_snapshot_literal", "s", "TXID_SNAPSHOT", [][]any{{"12:16:14"}}))
		identityBoth(t, p, b, c, fixture.Select("SELECT pg_get_expr(d.adbin,d.adrelid) AS expression FROM pg_attrdef AS d JOIN pg_class AS c ON c.oid=d.adrelid WHERE c.relname='legacy_snapshots'", []string{"expression"}, []string{"TEXT"}, [][]any{{"'12:16:14'::txid_snapshot"}}))
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type pg_snapshot: "bad"`, Context: "unnamed portal parameter $2 = '...'"}
		identityBoth(t, p, b, c, legacySnapshotError("INSERT INTO legacy_snapshots (id,s) VALUES (4,$1),(5,$2)", want, "12:13:", "bad"))
		identityBoth(t, p, b, c, fixture.Select("SELECT id FROM legacy_snapshots WHERE id=4", []string{"id"}, []string{"INT4"}, nil))
		var live, modern string
		if err := c.QueryRowContext(p.Ctx, "SELECT s::text,pg_current_snapshot()::text FROM legacy_snapshot_live").Scan(&live, &modern); err != nil || live != modern {
			t.Fatalf("symbolic live view %s/%s: %v", live, modern, err)
		}
	})
}

func TestReferenceLegacySnapshotFeatureSmoke(t *testing.T) {
	legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := snapshotConn(t, p, b)
		fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
		identityBoth(t, p, b, c, legacySnapshotStep(b, "SELECT txid_current_if_assigned() AS unassigned", []string{"unassigned"}, []string{"INT8"}, [][]any{{nil}}))
		identityBoth(t, p, b, c, legacySnapshotStep(b, "SELECT '12:16:14,14'::txid_snapshot AS s,txid_snapshot_xmin('12:16:14,14') AS lo,txid_snapshot_xmax('12:16:14,14') AS hi,txid_visible_in_snapshot(13,'12:16:14,14') AS visible", []string{"s", "lo", "hi", "visible"}, []string{"TXID_SNAPSHOT", "INT8", "INT8", "BOOL"}, [][]any{{"12:16:14", int64(12), int64(16), true}}))
		q := "SELECT x FROM txid_snapshot_xip($1) AS s(x) ORDER BY x"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		x := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			arg  any
			rows [][]any
		}{
			{"12:16:14", [][]any{{int64(14)}}},
			{"12:16:", nil},
			{nil, nil},
			{"12:16:14", [][]any{{int64(14)}}},
		} {
			fixture.RunStep(t, p.Ctx, x, b.Recorder, legacySnapshotStep(b, q, []string{"x"}, []string{"INT8"}, tc.rows, tc.arg))
		}
		var id int64
		if err := c.QueryRowContext(p.Ctx, "SELECT txid_current()").Scan(&id); err != nil {
			t.Fatal(err)
		}
		identityBoth(t, p, b, c, legacySnapshotStep(b, "SELECT txid_current_if_assigned() AS id,txid_status($1) AS status", []string{"id", "status"}, []string{"INT8", "TEXT"}, [][]any{{id, "in progress"}}, id))
		identityBoth(t, p, b, c, fixture.Select("SELECT pg_current_xact_id()::text AS id", []string{"id"}, []string{"TEXT"}, [][]any{{strconv.FormatUint(uint64(id), 10)}}))
		fixture.LockExec(t, p.Ctx, c, "COMMIT", 0)
		identityBoth(t, p, b, c, legacySnapshotStep(b, "SELECT txid_status($1) AS status", []string{"status"}, []string{"TEXT"}, [][]any{{"committed"}}, id))
	})
}
