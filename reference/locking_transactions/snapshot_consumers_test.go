package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func snapshotStoredRows(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	snapshotBoth(t, p, b, x, fixture.Select("SELECT id,s::text AS s FROM snapshot_store ORDER BY id", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "12:16:14"}, {int64(2), nil}, {int64(3), "12:16:14"}}))
	snapshotBoth(t, p, b, x, fixture.Select("SELECT s::text AS s,lo::text AS lo FROM snapshot_literal", []string{"s", "lo"}, []string{"TEXT", "TEXT"}, [][]any{{"12:16:14", "12"}}))
	snapshotBoth(t, p, b, x, fixture.Select("SELECT x::text AS x FROM snapshot_members ORDER BY x", []string{"x"}, []string{"TEXT"}, [][]any{{"10"}, {"14"}, {"15"}}))
}
func snapshotLiveView(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.LockExec(t, p.Ctx, c, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
	current := snapshotRead(t, p, c, nil)
	snapshotBoth(t, p, b, c, fixture.Select("SELECT s::text AS s FROM snapshot_live", []string{"s"}, []string{"TEXT"}, [][]any{{current}}))
	fixture.LockExec(t, p.Ctx, c, "COMMIT", 0)
}

func TestReferenceSnapshotConsumers(t *testing.T) {
	var replay []byte
	snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := snapshotConn(t, p, b)
		fixture.LockExec(t, p.Ctx, c, "CREATE TABLE snapshot_store(id integer,s pg_snapshot DEFAULT '12:16:14,14')", 0)
		fixture.LockExec(t, p.Ctx, c, "INSERT INTO snapshot_store VALUES(1,'12:16:14,14'),(2,NULL)", 2)
		fixture.LockExec(t, p.Ctx, c, "INSERT INTO snapshot_store(id) VALUES(3)", 1)
		fixture.LockExec(t, p.Ctx, c, "CREATE VIEW snapshot_literal AS SELECT '12:16:14,14'::pg_snapshot AS s,pg_snapshot_xmin('12:16:14,14') AS lo", 0)
		fixture.LockExec(t, p.Ctx, c, "CREATE VIEW snapshot_live AS SELECT pg_current_snapshot() AS s", 0)
		fixture.LockExec(t, p.Ctx, c, "CREATE VIEW snapshot_members AS SELECT pg_snapshot_xip('10:20:10,14,15') AS x", 0)
		snapshotStoredRows(t, p, b, c)
		snapshotLiveView(t, p, b, c)
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO snapshot_store VALUES(4,$1)", 1, []byte("10:20:10,14"))
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			snapshotBoth(t, p, b, c, fixture.Select("SELECT s::text AS s FROM snapshot_store WHERE id=4", []string{"s"}, []string{"TEXT"}, [][]any{{"10:20:10,14"}}))
			fixture.LockExec(t, p.Ctx, c, "DELETE FROM snapshot_store WHERE id=4", 1)
		}
		for _, tc := range []struct{ q, want string }{
			{"SELECT pg_get_expr(d.adbin,d.adrelid) AS definition FROM pg_attrdef d JOIN pg_class r ON d.adrelid=r.oid WHERE r.relname='snapshot_store' AND d.adnum=2", "'12:16:14'::pg_snapshot"},
			{"SELECT pg_get_viewdef('snapshot_live'::regclass,true) AS definition", " SELECT pg_current_snapshot() AS s;"},
		} {
			snapshotBoth(t, p, b, c, fixture.Select(tc.q, []string{"definition"}, []string{"TEXT"}, [][]any{{tc.want}}))
		}
		// A valid first row must not publish when the second row cannot decode.
		for _, prepared := range []bool{false, true} {
			q := "INSERT INTO snapshot_store VALUES(4,$1::pg_snapshot),(5,$2::pg_snapshot)"
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type pg_snapshot: "bad"`, Context: "unnamed portal parameter $2 = '...'"}
			s := fixture.ErrorDiagnostic(q, want, "10:20:", "bad")
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			snapshotStoredRows(t, p, b, c)
		}
		if !b.PG {
			retained, err := c.QueryContext(p.Ctx, "SELECT id,s::text AS s FROM snapshot_store ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			replay = append([]byte(nil), dump.Bytes()...)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(replay)); err != nil {
				t.Fatal(err)
			}
			snapshotStoredRows(t, p, b, c)
			snapshotLiveView(t, p, b, c)
			cols, err := retained.Columns()
			if err != nil || len(cols) != 2 || cols[0] != "id" || cols[1] != "s" {
				t.Fatalf("retained columns %v: %v", cols, err)
			}
			want := []struct {
				id int
				s  sql.NullString
			}{{1, sql.NullString{String: "12:16:14", Valid: true}}, {2, sql.NullString{}}, {3, sql.NullString{String: "12:16:14", Valid: true}}}
			for _, row := range want {
				if !retained.Next() {
					t.Fatal("retained cursor lost row", retained.Err())
				}
				var id int
				var s sql.NullString
				if err := retained.Scan(&id, &s); err != nil || id != row.id || s != row.s {
					t.Fatalf("retained %d/%v: %v", id, s, err)
				}
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained tail", retained.Err())
			}
			retained.Close()
			bad := "CREATE TABLE replacement(s pg_snapshot); INSERT INTO replacement VALUES('bad');"
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
			fixture.State(t, err, "22P02")
			snapshotStoredRows(t, p, b, c)
			// Fresh Driver: no engine/catalog/runtime pointer may leak into the dump.
			fresh := fixture.NewFixture(t, false, true)
			if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(replay)); err != nil {
				t.Fatal(err)
			}
			nb := fresh.Backends()[0]
			snapshotStoredRows(t, fresh, nb, nb.DB)
			fc := snapshotConn(t, fresh, nb)
			snapshotLiveView(t, fresh, nb, fc)
		} else if len(replay) != 0 {
			// Replay the actual native-emitted SQL, never an independently rebuilt dump.
			fixture.LockExec(t, p.Ctx, c, "DROP VIEW snapshot_members,snapshot_live,snapshot_literal", 0)
			fixture.LockExec(t, p.Ctx, c, "DROP TABLE snapshot_store", 0)
			if _, err := c.ExecContext(p.Ctx, string(replay)); err != nil {
				t.Fatal("PostgreSQL dump replay", err)
			}
			snapshotStoredRows(t, p, b, c)
			snapshotLiveView(t, p, b, c)
		}
	})
}
