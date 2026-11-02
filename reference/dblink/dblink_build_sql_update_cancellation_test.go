package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkBuildSQLUpdateCancellation(t *testing.T) {
	for _, route := range []string{"direct", "held"} {
		t.Run("relation-"+route, func(t *testing.T) {
			updateCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				updateKeys(t, p, b, c)
				held := updateHeld(t, p, c, updateCall)
				var x fixture.Executor = c
				if route == "held" {
					x = held
				}
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					rows, err := x.QueryContext(ctx, updateCall, "update_keys", "1", int64(1), "{1}", "{9}")
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
				if _, err := peer.ExecContext(p.Ctx, "LOCK TABLE update_keys IN ACCESS EXCLUSIVE MODE"); err != nil {
					t.Fatal(err)
				}
				// The public observer requires the actual source lock wait, not
				// elapsed time or merely a launched goroutine; PG cancels server-side.
				fixture.RelationLockActiveCancel(t, p, b, c, updateCall, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := x.QueryContext(ctx, updateCall, "update_keys", "1", int64(1), "{1}", "{9}")
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("ROLLBACK", 0))
				fixture.RunStep(t, p.Ctx, x, b.Recorder, updateSelect(updateCall, "UPDATE update_keys SET id = '9', k = 'a', payload = 'O''Brien' WHERE id = '9'", "update_keys", "1", int64(1), "{1}", "{9}"))
				// Independent exclusive ownership after the command/transaction
				// ends distinguishes complete cleanup from only a successful retry.
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("BEGIN", 0))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("LOCK TABLE update_keys IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("ROLLBACK", 0))
				run(fixture.Select("SELECT id FROM update_keys ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			})
		})
		t.Run("nested-"+route, func(t *testing.T) {
			updateCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE VIEW update_sleep AS SELECT 1 AS id,pg_sleep(60)::text AS payload", 0))
				const q = "SELECT dbupd_ext.dblink_build_sql_update('update_sleep','1'::int2vector,1,'{1}'::text[],'{9}'::text[]) AS sql"
				var x fixture.Executor = c
				if route == "held" {
					x = updateHeld(t, p, c, q)
				}
				fixture.SleepActiveCancelWithContext(t, p, b, c, q, "SQL statement \"SELECT id, payload FROM update_sleep WHERE id = '1'\"", nil, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := x.QueryContext(ctx, q)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				// Replace the interruptible source only after joining cancellation.
				run(fixture.Exec("CREATE OR REPLACE VIEW update_sleep AS SELECT 1 AS id,'awake'::text AS payload", 0))
				fixture.RunStep(t, p.Ctx, x, b.Recorder, updateSelect(q, "UPDATE update_sleep SET id = '9', payload = 'awake' WHERE id = '9'"))
				run(fixture.Select("SELECT 7 AS ok", []string{"ok"}, []string{"INT4"}, [][]any{{int64(7)}}))
			})
		})
	}
}

func TestReferenceDblinkBuildSQLUpdateAtomicity(t *testing.T) {
	for _, route := range []string{"direct", "prepared", "held"} {
		t.Run("failure-"+route, func(t *testing.T) {
			updateCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{
					"CREATE TABLE update_atomic_source(id integer,payload text)",
					"CREATE TABLE update_atomic_marker(id integer)",
					"CREATE FUNCTION update_atomic_fail() RETURNS text LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO update_atomic_marker VALUES(5); INSERT INTO update_atomic_source VALUES(2,'effect'); RETURN dbupd_ext.dblink_build_sql_update('update_atomic_source','1'::int2vector,1,ARRAY['99'],ARRAY['9']); END$$",
				} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO update_atomic_source VALUES(1,'original')", 1))
				const q = "SELECT update_atomic_fail() AS sql"
				var x fixture.Executor = c
				if route == "held" {
					x = updateHeld(t, p, c, q)
				}
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("SAVEPOINT update_atomic", 0))
				s := updateError(q, "21000", "source row not found")
				s.Diagnostic.Context = "PL/pgSQL function update_atomic_fail() line 1 at RETURN"
				s.Prepared = route == "prepared"
				fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
				run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
				run(fixture.Exec("ROLLBACK TO update_atomic", 0))
				run(fixture.Select("SELECT id FROM update_atomic_marker ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
				run(fixture.Select("SELECT id,payload FROM update_atomic_source ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "original"}}))
				run(updateSelect(updateCall, "UPDATE update_atomic_source SET id = '9', payload = 'original' WHERE id = '9'", "update_atomic_source", "1", int64(1), "{1}", "{9}"))
				run(fixture.Exec("INSERT INTO update_atomic_source VALUES(3,'private')", 1))
				run(updateSelect(updateCall, "UPDATE update_atomic_source SET id = '9', payload = 'private' WHERE id = '9'", "update_atomic_source", "1", int64(1), "{3}", "{9}"))
				run(fixture.Exec("ROLLBACK TO update_atomic", 0))
				run(updateError(updateCall, "21000", "source row not found", "update_atomic_source", "1", int64(1), "{3}", "{9}"))
				run(fixture.Exec("ROLLBACK", 0))
			})
		})
	}
	for _, route := range []string{"direct", "held"} {
		t.Run("cancel-"+route, func(t *testing.T) {
			updateCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{
					"CREATE TABLE update_cancel_marker(id integer)",
					"CREATE VIEW update_cancel_source AS SELECT 1 AS id,pg_sleep(60)::text AS payload",
					"CREATE FUNCTION update_cancel_write() RETURNS text LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO update_cancel_marker VALUES(5); RETURN dbupd_ext.dblink_build_sql_update('update_cancel_source','1'::int2vector,1,ARRAY['1'],ARRAY['9']); END$$",
				} {
					run(fixture.Exec(q, 0))
				}
				const q = "SELECT update_cancel_write() AS sql"
				var x fixture.Executor = c
				if route == "held" {
					x = updateHeld(t, p, c, q)
				}
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("SAVEPOINT update_cancel", 0))
				provenance := "SQL statement \"SELECT id, payload FROM update_cancel_source WHERE id = '1'\"\nPL/pgSQL function update_cancel_write() line 1 at RETURN"
				fixture.SleepActiveCancelWithContext(t, p, b, c, q, provenance, nil, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := x.QueryContext(ctx, q)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				run(fixture.Exec("ROLLBACK TO update_cancel", 0))
				run(fixture.Select("SELECT id FROM update_cancel_marker ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("CREATE OR REPLACE VIEW update_cancel_source AS SELECT 1 AS id,'awake'::text AS payload", 0))
				fixture.RunStep(t, p.Ctx, x, b.Recorder, updateSelect(q, "UPDATE update_cancel_source SET id = '9', payload = 'awake' WHERE id = '9'"))
				run(fixture.Select("SELECT id FROM update_cancel_marker ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}}))
			})
		})
	}
}
