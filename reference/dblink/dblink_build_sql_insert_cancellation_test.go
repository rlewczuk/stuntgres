package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkBuildSQLInsertCancellation(t *testing.T) {
	for _, route := range []string{"direct", "held"} {
		t.Run("relation-"+route, func(t *testing.T) {
			insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				insertKeys(t, p, b, c)
				held := insertHeld(t, p, c, insertCall)
				var x fixture.Executor = c
				if route == "held" {
					x = held
				}
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					rows, err := x.QueryContext(ctx, insertCall, "insert_keys", "1", int64(1), "{1}", "{9}")
					if rows != nil {
						rows.Close()
						t.Fatal("precancel exposed rows")
					}
					fixture.LexicalOrdinary(t, err, context.Canceled)
				}
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				if _, err := peer.ExecContext(p.Ctx, "BEGIN"); err != nil {
					t.Fatal(err)
				}
				defer peer.ExecContext(context.Background(), "ROLLBACK")
				if _, err := peer.ExecContext(p.Ctx, "LOCK TABLE insert_keys IN ACCESS EXCLUSIVE MODE"); err != nil {
					t.Fatal(err)
				}
				// The public observer requires the actual source lock wait, not
				// elapsed time or merely a launched goroutine; PG cancels server-side.
				fixture.RelationLockActiveCancel(t, p, b, c, insertCall, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := x.QueryContext(ctx, insertCall, "insert_keys", "1", int64(1), "{1}", "{9}")
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("ROLLBACK", 0))
				fixture.RunStep(t, p.Ctx, x, b.Recorder, insertSelect(insertCall, "INSERT INTO insert_keys(id,k,payload) VALUES('9','a','O''Brien')", "insert_keys", "1", int64(1), "{1}", "{9}"))
				// Independent exclusive ownership after the command/transaction
				// ends distinguishes complete cleanup from only a successful retry.
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("BEGIN", 0))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("LOCK TABLE insert_keys IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("ROLLBACK", 0))
				run(fixture.Select("SELECT id FROM insert_keys ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			})
		})
		t.Run("nested-"+route, func(t *testing.T) {
			insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE VIEW insert_sleep AS SELECT 1 AS id,pg_sleep(60)::text AS payload", 0))
				const q = "SELECT dbins_ext.dblink_build_sql_insert('insert_sleep','1'::int2vector,1,'{1}'::text[],'{9}'::text[]) AS sql"
				var x fixture.Executor = c
				if route == "held" {
					x = insertHeld(t, p, c, q)
				}
				fixture.SleepActiveCancelWithContext(t, p, b, c, q, "SQL statement \"SELECT id, payload FROM insert_sleep WHERE id = '1'\"", nil, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := x.QueryContext(ctx, q)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				// Replace the interruptible source only after joining cancellation.
				run(fixture.Exec("CREATE OR REPLACE VIEW insert_sleep AS SELECT 1 AS id,'awake'::text AS payload", 0))
				fixture.RunStep(t, p.Ctx, x, b.Recorder, insertSelect(q, "INSERT INTO insert_sleep(id,payload) VALUES('9','awake')"))
				run(fixture.Select("SELECT 7 AS ok", []string{"ok"}, []string{"INT4"}, [][]any{{int64(7)}}))
			})
		})
	}
}

func TestReferenceDblinkBuildSQLInsertAtomicity(t *testing.T) {
	for _, route := range []string{"direct", "prepared", "held"} {
		t.Run("failure-"+route, func(t *testing.T) {
			insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{
					"CREATE TABLE insert_atomic_source(id integer,payload text)",
					"CREATE TABLE insert_atomic_marker(id integer)",
					"CREATE FUNCTION insert_atomic_fail() RETURNS text LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO insert_atomic_marker VALUES(5); INSERT INTO insert_atomic_source VALUES(2,'effect'); RETURN dbins_ext.dblink_build_sql_insert('insert_atomic_source','1'::int2vector,1,ARRAY['99'],ARRAY['9']); END$$",
				} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO insert_atomic_source VALUES(1,'original')", 1))
				const q = "SELECT insert_atomic_fail() AS sql"
				var x fixture.Executor = c
				if route == "held" {
					x = insertHeld(t, p, c, q)
				}
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("SAVEPOINT insert_atomic", 0))
				s := insertError(q, "21000", "source row not found")
				s.Diagnostic.Context = "PL/pgSQL function insert_atomic_fail() line 1 at RETURN"
				s.Prepared = route == "prepared"
				fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
				run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
				run(fixture.Exec("ROLLBACK TO insert_atomic", 0))
				run(fixture.Select("SELECT id FROM insert_atomic_marker ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
				run(fixture.Select("SELECT id,payload FROM insert_atomic_source ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "original"}}))
				run(insertSelect(insertCall, "INSERT INTO insert_atomic_source(id,payload) VALUES('9','original')", "insert_atomic_source", "1", int64(1), "{1}", "{9}"))
				run(fixture.Exec("INSERT INTO insert_atomic_source VALUES(3,'private')", 1))
				run(insertSelect(insertCall, "INSERT INTO insert_atomic_source(id,payload) VALUES('9','private')", "insert_atomic_source", "1", int64(1), "{3}", "{9}"))
				run(fixture.Exec("ROLLBACK TO insert_atomic", 0))
				run(insertError(insertCall, "21000", "source row not found", "insert_atomic_source", "1", int64(1), "{3}", "{9}"))
				run(fixture.Exec("ROLLBACK", 0))
			})
		})
	}
	for _, route := range []string{"direct", "held"} {
		t.Run("cancel-"+route, func(t *testing.T) {
			insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{
					"CREATE TABLE insert_cancel_marker(id integer)",
					"CREATE VIEW insert_cancel_source AS SELECT 1 AS id,pg_sleep(60)::text AS payload",
					"CREATE FUNCTION insert_cancel_write() RETURNS text LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO insert_cancel_marker VALUES(5); RETURN dbins_ext.dblink_build_sql_insert('insert_cancel_source','1'::int2vector,1,ARRAY['1'],ARRAY['9']); END$$",
				} {
					run(fixture.Exec(q, 0))
				}
				const q = "SELECT insert_cancel_write() AS sql"
				var x fixture.Executor = c
				if route == "held" {
					x = insertHeld(t, p, c, q)
				}
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("SAVEPOINT insert_cancel", 0))
				provenance := "SQL statement \"SELECT id, payload FROM insert_cancel_source WHERE id = '1'\"\nPL/pgSQL function insert_cancel_write() line 1 at RETURN"
				fixture.SleepActiveCancelWithContext(t, p, b, c, q, provenance, nil, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := x.QueryContext(ctx, q)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				run(fixture.Exec("ROLLBACK TO insert_cancel", 0))
				run(fixture.Select("SELECT id FROM insert_cancel_marker ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("CREATE OR REPLACE VIEW insert_cancel_source AS SELECT 1 AS id,'awake'::text AS payload", 0))
				fixture.RunStep(t, p.Ctx, x, b.Recorder, insertSelect(q, "INSERT INTO insert_cancel_source(id,payload) VALUES('9','awake')"))
				run(fixture.Select("SELECT id FROM insert_cancel_marker ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}}))
			})
		})
	}
}
