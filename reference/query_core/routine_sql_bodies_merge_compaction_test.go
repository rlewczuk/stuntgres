package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// The target deliberately never changes layout. Only the source loses an
// unused leading attribute, so target rebinding cannot repair frozen source
// slots accidentally. Each invocation starts with v=0, not an already merged row.
func TestReferenceRoutineSQLBodiesMergeSourceCompaction(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "dcr_merge")
	sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE SCHEMA dcr_merge",
			"CREATE TABLE dcr_merge.target(id integer,v integer)",
			"CREATE TABLE dcr_merge.source(unused integer,id integer,v integer)",
			"CREATE FUNCTION dcr_merge.run() RETURNS void LANGUAGE SQL BEGIN ATOMIC MERGE INTO dcr_merge.target t USING dcr_merge.source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v; END")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dcr_merge.target VALUES(1,0)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dcr_merge.source VALUES(99,1,7)", 1))

		exercise := func(backend fixture.Backend, commit bool) {
			t.Helper()
			// Every replacement/replay gets fresh public handles. A notice-enabled
			// connection also proves unused-column removal is not a CASCADE action.
			var notices []fixture.Diagnostic
			_, conn, rec := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			defer conn.Close()
			backend.Recorder = rec
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			exec := func(q string) { run(fixture.Exec(q, 0)) }
			identitySQL := "SELECT 'dcr_merge.run()'::regprocedure::oid::bigint AS routine_oid,'dcr_merge.source'::regclass::oid::bigint AS source_oid,'dcr_merge.target'::regclass::oid::bigint AS target_oid"
			var routineID, sourceID, targetID int64
			if err := conn.QueryRowContext(p.Ctx, identitySQL).Scan(&routineID, &sourceID, &targetID); err != nil {
				t.Fatal(err)
			}
			edges := fixture.Select("SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='dcr_merge.run()'::regprocedure AND refclassid='pg_class'::regclass AND refobjid='dcr_merge.source'::regclass ORDER BY refobjsubid,deptype",
				[]string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"},
				[]string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"},
				[][]any{{int64(1255), routineID, int64(0), int64(1259), sourceID, int64(2), "n"}, {int64(1255), routineID, int64(0), int64(1259), sourceID, int64(3), "n"}})
			state := func(compacted bool) {
				t.Helper()
				run(fixture.Select(identitySQL, []string{"routine_oid", "source_oid", "target_oid"}, []string{"INT8", "INT8", "INT8"}, [][]any{{routineID, sourceID, targetID}}))
				run(edges)
				attrs := [][]any{{"unused", int64(1)}, {"id", int64(2)}, {"v", int64(3)}}
				if compacted {
					attrs = [][]any{{"id", int64(2)}, {"v", int64(3)}}
					run(fixture.Select("SELECT * FROM dcr_merge.source", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}}))
				} else {
					run(fixture.Select("SELECT * FROM dcr_merge.source", []string{"unused", "id", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(99), int64(1), int64(7)}}))
				}
				run(fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='dcr_merge.source'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, attrs))
				run(fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='dcr_merge.target'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, [][]any{{"id", int64(1)}, {"v", int64(2)}}))
			}
			target := func(value int64) {
				t.Helper()
				run(fixture.Select("SELECT id,v FROM dcr_merge.target", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), value}}))
			}
			quiet := func() {
				t.Helper()
				if len(notices) != 0 {
					t.Fatalf("unexpected notices: %#v", notices)
				}
			}
			query := "SELECT dcr_merge.run() IS NULL AS done"
			stmt, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal("prepare before source compaction", err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
			invoke := func(x fixture.Executor) {
				t.Helper()
				run(fixture.Exec("UPDATE dcr_merge.target SET v=0", 1))
				fixture.RunStep(t, p.Ctx, x, backend.Recorder, fixture.Select(query, []string{"done"}, []string{"BOOL"}, [][]any{{true}}))
				target(7)
			}
			dropUnused := func() {
				t.Helper()
				exec("ALTER TABLE dcr_merge.source DROP COLUMN unused RESTRICT")
				quiet()
				state(true)
			}
			restrictUsed := func(inTransaction bool) {
				t.Helper()
				for _, column := range []string{"id", "v"} {
					for _, prepared := range []bool{false, true} {
						if inTransaction {
							exec("SAVEPOINT used_column")
						}
						diagnostic := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop column "+column+" of table dcr_merge.source because other objects depend on it")
						diagnostic.Detail = "function dcr_merge.run() depends on column " + column + " of table dcr_merge.source"
						diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
						s := fixture.ErrorDiagnostic("ALTER TABLE dcr_merge.source DROP COLUMN "+column+" RESTRICT", diagnostic)
						s.Prepared, s.ErrorStage = prepared, "execute"
						run(s)
						if inTransaction {
							exec("ROLLBACK TO used_column")
							exec("RELEASE used_column")
						}
						state(true)
						target(7)
						quiet()
					}
				}
			}

			state(false)
			target(0)
			quiet()
			invoke(held)
			invoke(conn)
			run(fixture.Exec("UPDATE dcr_merge.target SET v=0", 1))
			exec("BEGIN")
			transactionOpen := true
			defer func() {
				if transactionOpen {
					_, _ = conn.ExecContext(p.Ctx, "ROLLBACK")
				}
			}()
			if commit {
				dropUnused()
				exec("COMMIT")
				transactionOpen = false
				invoke(held)
				invoke(held)
				invoke(conn)
				restrictUsed(false)
				state(true)
				return
			}
			exec("SAVEPOINT before_drop")
			dropUnused()
			invoke(held)
			invoke(held)
			invoke(conn)
			restrictUsed(true)
			exec("ROLLBACK TO before_drop")
			state(false)
			target(0)
			dropUnused()
			invoke(held)
			exec("ROLLBACK")
			transactionOpen = false
			state(false)
			target(0)
			invoke(held)
			run(fixture.Exec("UPDATE dcr_merge.target SET v=0", 1))
			quiet()
		}
		// Each check restores the original three-column baseline before Dump.
		// The incumbent helper performs two native replacements and runs each
		// unchanged emitted stream on PostgreSQL; every phase compacts anew.
		check := func(_ *sql.Conn, backend fixture.Backend) { exercise(backend, false) }
		finish := func(_ *sql.Conn, backend fixture.Backend) { exercise(backend, true) }
		sqlBodiesReplayTwice(t, p, b, c, "dcr_merge", check, finish)
	})
}
