package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkBuildSQLInsertSourceSemantics(t *testing.T) {
	t.Run("sources", func(t *testing.T) {
		insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{
				"CREATE TABLE insert_base(id integer,payload text)",
				"CREATE VIEW insert_source AS SELECT id,payload||' view' AS payload FROM insert_base",
				"CREATE TABLE insert_parent(id integer,payload text)",
				"CREATE TABLE insert_child() INHERITS(insert_parent)",
				"CREATE TABLE insert_collation(id text COLLATE \"C\",payload text)",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO insert_base VALUES(1,'base')", 1))
			run(fixture.Exec("INSERT INTO insert_child VALUES(7,'child')", 1))
			run(fixture.Exec("INSERT INTO insert_collation VALUES('A','upper'),('a','lower')", 2))
			held := insertHeld(t, p, c, insertCall)
			for _, tc := range []struct{ relation, source, target, want string }{
				{"insert_source", "{01}", "{9}", "INSERT INTO insert_source(id,payload) VALUES('9','base view')"},
				{"insert_parent", "{007}", "{9}", "INSERT INTO insert_parent(id,payload) VALUES('9','child')"},
				{"insert_child", "{7}", "{9}", "INSERT INTO insert_child(id,payload) VALUES('9','child')"},
				{"insert_collation", "{A}", "{remote}", "INSERT INTO insert_collation(id,payload) VALUES('remote','upper')"},
			} {
				s := insertSelect(insertCall, tc.want, tc.relation, "1", int64(1), tc.source, tc.target)
				insertBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			run(fixture.Exec("INSERT INTO insert_parent VALUES(7,'parent')", 1))
			insertBoth(t, p, b, c, insertError(insertCall, "21000", "source criteria matched more than one record", "insert_parent", "1", int64(1), "{7}", "{9}"))
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("SAVEPOINT insert_private", 0))
			run(fixture.Exec("INSERT INTO insert_base VALUES(2,'private')", 1))
			s := insertSelect(insertCall, "INSERT INTO insert_base(id,payload) VALUES('9','private')", "insert_base", "1", int64(1), "{2}", "{9}")
			insertBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Exec("ROLLBACK TO insert_private", 0))
			missing := insertError(insertCall, "21000", "source row not found", "insert_base", "1", int64(1), "{2}", "{9}")
			run(missing)
			run(fixture.Exec("ROLLBACK TO insert_private", 0))
			run(fixture.Prepared(missing))
			run(fixture.Exec("ROLLBACK", 0))
		})
	})
	t.Run("drain", func(t *testing.T) {
		insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{
				"CREATE TABLE insert_drain_rows(n integer)",
				"CREATE FUNCTION insert_late_error(n integer) RETURNS text LANGUAGE plpgsql VOLATILE AS $$BEGIN IF n=3 THEN RAISE EXCEPTION 'late source error'; END IF; RETURN n::text; END$$",
				"CREATE VIEW insert_drain AS SELECT 1 AS id,insert_late_error(n) AS payload FROM insert_drain_rows ORDER BY n",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO insert_drain_rows VALUES(1),(2),(3)", 3))
			s := insertError(insertCall, "P0001", "late source error", "insert_drain", "1", int64(1), "{1}", "{9}")
			// The error is present regardless of executor enumeration order; no
			// choice of either first matching tuple may suppress source failure.
			s.Diagnostic.Context = "PL/pgSQL function insert_late_error(integer) line 1 at RAISE\nSQL statement \"SELECT id, payload FROM insert_drain WHERE id = '1'\""
			insertBoth(t, p, b, c, s)
			held := insertHeld(t, p, c, insertCall)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Exec("DELETE FROM insert_drain_rows WHERE n=3", 1))
			insertBoth(t, p, b, c, insertError(insertCall, "21000", "source criteria matched more than one record", "insert_drain", "1", int64(1), "{1}", "{9}"))
		})
	})
}

func TestReferenceDblinkBuildSQLInsertSnapshot(t *testing.T) {
	for _, route := range []string{"direct", "held"} {
		t.Run("stable-"+route, func(t *testing.T) {
			insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{
					"CREATE TABLE insert_snapshot(id integer,payload text)",
					"CREATE FUNCTION insert_snapshot_write() RETURNS text[] LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO insert_snapshot VALUES(1,'fresh'); RETURN ARRAY['1']; END$$",
					"CREATE FUNCTION insert_snapshot_stable() RETURNS text LANGUAGE plpgsql STABLE AS $$DECLARE before_count bigint; built_sql text; after_count bigint; BEGIN SELECT count(*) INTO before_count FROM insert_snapshot; built_sql := dbins_ext.dblink_build_sql_insert('insert_snapshot','1'::int2vector,1,insert_snapshot_write(),ARRAY['9']); SELECT count(*) INTO after_count FROM insert_snapshot; RETURN before_count::text||'|'||built_sql||'|'||after_count::text; END$$",
				} {
					run(fixture.Exec(q, 0))
				}
				q := "SELECT insert_snapshot_stable() AS sql"
				var x fixture.Executor = c
				if route == "held" {
					x = insertHeld(t, p, c, q)
				}
				fixture.RunStep(t, p.Ctx, x, b.Recorder, insertSelect(q, "0|INSERT INTO insert_snapshot(id,payload) VALUES('9','fresh')|0"))
				run(fixture.Select("SELECT id,payload FROM insert_snapshot ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "fresh"}}))
			})
		})
	}
	for _, isolation := range []string{"READ COMMITTED", "REPEATABLE READ", "SERIALIZABLE"} {
		for _, route := range []string{"direct", "held"} {
			t.Run(isolation+"-"+route, func(t *testing.T) {
				insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE insert_peer(id integer,payload text)", 0))
					held := insertHeld(t, p, c, insertCall)
					var x fixture.Executor = c
					if route == "held" {
						x = held
					}
					run(fixture.Exec("BEGIN ISOLATION LEVEL "+isolation, 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					// Establish the transaction frontier on an unrelated table so
					// peer can acquire AccessExclusive on the actual source.
					run(fixture.Select("SELECT count(*) AS n FROM pg_namespace WHERE nspname='dbins_ext'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					if _, err := peer.ExecContext(p.Ctx, "BEGIN"); err != nil {
						t.Fatal(err)
					}
					defer peer.ExecContext(context.Background(), "ROLLBACK")
					if _, err := peer.ExecContext(p.Ctx, "LOCK TABLE insert_peer IN ACCESS EXCLUSIVE MODE"); err != nil {
						t.Fatal(err)
					}
					pid, peerPID, oid := 0, 0, int64(0)
					if b.PG {
						pid, peerPID = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, peer)
						oid = fixture.LockOID(t, p.Ctx, "insert_peer")
					}
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					finished := make(chan struct{})
					join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						rows, err := x.QueryContext(ctx, insertCall, "insert_peer", "1", int64(1), "{1}", "{9}")
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					joined := false
					defer func() {
						cancel()
						if !joined {
							got := join()
							if got.Rows != nil {
								got.Rows.Close()
							}
						}
					}()
					fixture.RelationLockPending(t, p, b, pid, peerPID, oid, "AccessShareLock", finished)
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("INSERT INTO insert_peer VALUES(1,'committed')", 1))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("COMMIT", 0))
					got := join()
					joined = true
					if isolation != "READ COMMITTED" {
						if got.Rows != nil {
							got.Rows.Close()
						}
						fixture.AssertDiagnostic(t, got.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "21000", Message: "source row not found"})
					} else {
						if got.Err != nil {
							t.Fatal(got.Err)
						}
						if got.Rows == nil {
							t.Fatal("missing fresh source row")
						}
						defer got.Rows.Close()
						if !got.Rows.Next() {
							t.Fatal(got.Rows.Err())
						}
						var generated string
						if err := got.Rows.Scan(&generated); err != nil || generated != "INSERT INTO insert_peer(id,payload) VALUES('9','committed')" {
							t.Fatal(generated, err)
						}
						if got.Rows.Next() || got.Rows.Err() != nil {
							t.Fatal("source result tail", got.Rows.Err())
						}
						if err := got.Rows.Close(); err != nil {
							t.Fatal(err)
						}
					}
					run(fixture.Exec("ROLLBACK", 0))
					run(fixture.Select("SELECT id,payload FROM insert_peer ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "committed"}}))
				})
			})
		}
	}
}
