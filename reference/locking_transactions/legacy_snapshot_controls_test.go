package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func legacySnapshotStored(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	identityBoth(t, p, b, x, fixture.Select("SELECT id,s::text AS s,pg_typeof(s)::text AS kind FROM legacy_control_store ORDER BY id", []string{"id", "s", "kind"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "12:16:14", "txid_snapshot"}, {int64(2), nil, "txid_snapshot"}, {int64(3), "12:16:14", "txid_snapshot"}}))
	identityBoth(t, p, b, x, legacySnapshotStoredStep(b, "SELECT s FROM legacy_control_literal", "s", "TXID_SNAPSHOT", [][]any{{"12:16:14"}}))
	identityBoth(t, p, b, x, legacySnapshotStoredStep(b, "SELECT x FROM legacy_control_members ORDER BY x", "x", "INT8", [][]any{{int64(13)}, {int64(15)}, {int64(18)}}))
}

func legacySnapshotLive(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.LockExec(t, p.Ctx, c, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
	s := snapshotRead(t, p, c, nil)
	identityBoth(t, p, b, c, fixture.Select("SELECT s::text AS s FROM legacy_control_live", []string{"s"}, []string{"TEXT"}, [][]any{{s}}))
	fixture.LockExec(t, p.Ctx, c, "COMMIT", 0)
}

func TestReferenceLegacySnapshotControls(t *testing.T) {
	var replay []byte
	legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := snapshotConn(t, p, b)
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE legacy_control_store(id integer,s txid_snapshot DEFAULT '12:16:14,14')", 0),
			fixture.Exec("INSERT INTO legacy_control_store VALUES(1,'12:16:14,14'),(2,NULL)", 2),
			fixture.Exec("INSERT INTO legacy_control_store(id) VALUES(3)", 1),
			fixture.Exec("CREATE VIEW legacy_control_literal AS SELECT '12:16:14,14'::txid_snapshot AS s", 0),
			fixture.Exec("CREATE VIEW legacy_control_members AS SELECT txid_snapshot_xip('12:20:13,15,18') AS x", 0),
			fixture.Exec("CREATE VIEW legacy_control_live AS SELECT txid_current_snapshot() AS s", 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		legacySnapshotStored(t, p, b, c)
		legacySnapshotLive(t, p, b, c)
		identityBoth(t, p, b, c, fixture.Select("SELECT pg_get_viewdef('legacy_control_live'::regclass,true) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{" SELECT txid_current_snapshot() AS s;"}}))
		if !b.PG {
			retained, err := c.QueryContext(p.Ctx, "SELECT s FROM legacy_control_store ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			replay = append([]byte(nil), dump.Bytes()...)
			if !strings.Contains(string(replay), "txid_snapshot") || !strings.Contains(string(replay), "txid_current_snapshot()") {
				t.Fatal("dump lost nominal or symbolic legacy identity")
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(replay)); err != nil {
				t.Fatal(err)
			}
			legacySnapshotStored(t, p, b, c)
			legacySnapshotLive(t, p, b, c)
			for _, want := range []any{"12:16:14", nil, "12:16:14"} {
				if !retained.Next() {
					t.Fatal("retained snapshot cursor lost row", retained.Err())
				}
				var v any
				if err := retained.Scan(&v); err != nil {
					t.Fatal(err)
				}
				if want == nil {
					if v != nil {
						t.Fatal("retained NULL", v)
					}
				} else if raw, ok := v.([]byte); !ok || string(raw) != want {
					t.Fatalf("retained nominal snapshot %T %v want %v", v, v, want)
				}
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained cursor tail", retained.Err())
			}
			retained.Close()
			bad := "CREATE TABLE replacement(s txid_snapshot); INSERT INTO replacement VALUES('bad');"
			fixture.State(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), "22P02")
			legacySnapshotStored(t, p, b, c)
			fresh := fixture.NewFixture(t, false, true)
			if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(replay)); err != nil {
				t.Fatal(err)
			}
			nb := fresh.Backends()[0]
			legacySnapshotStored(t, fresh, nb, nb.DB)
			legacySnapshotLive(t, fresh, nb, snapshotConn(t, fresh, nb))
		} else {
			if len(replay) == 0 {
				t.Fatal("missing actual native dump for PostgreSQL replay")
			}
			fixture.LockExec(t, p.Ctx, c, "DROP VIEW legacy_control_members,legacy_control_live,legacy_control_literal", 0)
			fixture.LockExec(t, p.Ctx, c, "DROP TABLE legacy_control_store", 0)
			if _, err := c.ExecContext(p.Ctx, string(replay)); err != nil {
				t.Fatal("actual emitted SQL replay", err)
			}
			legacySnapshotStored(t, p, b, c)
			legacySnapshotLive(t, p, b, c)
		}
	})
}

func TestReferenceLegacySnapshotStatusRetirement(t *testing.T) {
	legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, o := snapshotConn(t, p, b), snapshotConn(t, p, b)
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE legacy_status_store(v integer)", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO legacy_status_store VALUES(7)", 1)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		var aborted int64
		if err := a.QueryRowContext(p.Ctx, "SELECT txid_current()").Scan(&aborted); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, a, b.Recorder, legacySnapshotError("SELECT 1/$1::integer", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}, int64(0)))
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		identityBoth(t, p, b, o, legacySnapshotStep(b, "SELECT txid_status($1) AS s", []string{"s"}, []string{"TEXT"}, [][]any{{"aborted"}}, aborted))
		if b.PG {
			return
		}
		for _, restore := range []bool{false, true} {
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			var id int64
			if err := a.QueryRowContext(p.Ctx, "SELECT txid_current()").Scan(&id); err != nil {
				t.Fatal(err)
			}
			fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
			fixture.State(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE invalid(")), "42601")
			identityBoth(t, p, b, o, legacySnapshotStep(b, "SELECT txid_status($1) AS s", []string{"s"}, []string{"TEXT"}, [][]any{{"committed"}}, id))
			identityBoth(t, p, b, o, fixture.Select("SELECT v FROM legacy_status_store", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			var err error
			if restore {
				err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE legacy_status_store(v integer); INSERT INTO legacy_status_store VALUES(7);"))
			} else {
				err = stuntgres.Reset(p.Ctx, p.Native)
			}
			if err != nil {
				t.Fatal(err)
			}
			identityBoth(t, p, b, o, legacySnapshotStep(b, "SELECT txid_status($1) AS s", []string{"s"}, []string{"TEXT"}, [][]any{{nil}}, id))
			if !restore {
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE legacy_status_store(v integer)", 0)
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO legacy_status_store VALUES(7)", 1)
			}
		}
	})
}
