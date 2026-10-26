package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Only local FOREIGN TABLE metadata loses a column; the real remote SQL table
// keeps all three columns. Frozen input bridges must still open actual FDW scans.
func TestReferenceFDWRoutineInputCompaction(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.routine_input(unused integer,v integer,tail integer)", 0)
		fdwSourceExec(t, p, b, reader, run, "INSERT INTO fdw_source.routine_input VALUES(99,7,9)", 1)
		run(fixture.Exec("CREATE FOREIGN TABLE dcr_foreign_input(unused integer,v integer,tail integer) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'routine_input')", 0))
		run(fixture.Exec("CREATE FUNCTION dcr_foreign_input_run() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT v FROM dcr_foreign_input; END", 0))
		exercise := func(conn *sql.Conn, commit bool) {
			t.Helper()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			exec := func(q string) { run(fixture.Exec(q, 0)) }
			var routineID, sourceID int64
			identitySQL := "SELECT 'dcr_foreign_input_run()'::regprocedure::oid::bigint AS routine_oid,'dcr_foreign_input'::regclass::oid::bigint AS source_oid"
			if err := conn.QueryRowContext(p.Ctx, identitySQL).Scan(&routineID, &sourceID); err != nil {
				t.Fatal(err)
			}
			remote := func() {
				t.Helper()
				fdwWriteSourceRows(t, p, b, reader, fixture.Select("SELECT unused,v,tail FROM fdw_source.routine_input", []string{"unused", "v", "tail"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(99), int64(7), int64(9)}}))
			}
			state := func(compacted bool) {
				t.Helper()
				run(fixture.Select(identitySQL, []string{"routine_oid", "source_oid"}, []string{"INT8", "INT8"}, [][]any{{routineID, sourceID}}))
				run(fixture.Select("SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='dcr_foreign_input_run()'::regprocedure AND refclassid='pg_class'::regclass AND refobjid='dcr_foreign_input'::regclass ORDER BY refobjsubid,deptype",
					[]string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"},
					[][]any{{int64(1255), routineID, int64(0), int64(1259), sourceID, int64(2), "n"}}))
				attrs := [][]any{{"unused", int64(1)}, {"v", int64(2)}, {"tail", int64(3)}}
				if compacted {
					attrs = [][]any{{"v", int64(2)}, {"tail", int64(3)}}
				}
				run(fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='dcr_foreign_input'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, attrs))
				remote()
			}
			query := "SELECT dcr_foreign_input_run() AS v"
			stmt, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal("prepare before foreign input compaction", err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
			invoke := func(x fixture.Executor, inTransaction bool) {
				t.Helper()
				before := reader.snapshot()
				fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
				if !b.PG {
					after := reader.snapshot()
					if after.begins != before.begins+1 || after.ends != before.ends+1 || after.openRows != before.openRows || after.openCursors != before.openCursors || !inTransaction && after.openPools != before.openPools {
						t.Fatal("frozen input did not complete its actual foreign scan", before, after)
					}
				}
			}
			progress := func() {
				t.Helper()
				run(fixture.Select("SELECT v,tail FROM dcr_foreign_input", []string{"v", "tail"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
			}
			restrict := func(inTransaction bool) {
				d := fdwDiagnostic("2BP01", "cannot drop column v of foreign table dcr_foreign_input because other objects depend on it")
				d.Detail = "function dcr_foreign_input_run() depends on column v of foreign table dcr_foreign_input"
				d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				for _, prepared := range []bool{false, true} {
					if inTransaction {
						exec("SAVEPOINT used_input")
					}
					s := fixture.ErrorDiagnostic("ALTER FOREIGN TABLE dcr_foreign_input DROP v RESTRICT", d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					if inTransaction {
						exec("ROLLBACK TO used_input")
						exec("RELEASE used_input")
					}
					state(true)
				}
			}
			state(false)
			invoke(held, false)
			invoke(conn, false)
			exec("BEGIN")
			open := true
			defer func() {
				if open {
					_, _ = conn.ExecContext(p.Ctx, "ROLLBACK")
				}
			}()
			exec("SAVEPOINT before_drop")
			exec("ALTER FOREIGN TABLE dcr_foreign_input DROP unused RESTRICT")
			state(true)
			if commit {
				exec("COMMIT")
				open = false
				invoke(held, false)
				invoke(held, false)
				invoke(conn, false)
				restrict(false)
				progress()
				remote()
				return
			}
			invoke(held, true)
			invoke(held, true)
			invoke(conn, true)
			restrict(true)
			progress()
			exec("ROLLBACK TO before_drop")
			state(false)
			exec("ROLLBACK")
			open = false
			invoke(held, false)
			state(false)
			progress()
			if !b.PG {
				after := reader.snapshot()
				if after.openPools != 0 || after.openRows != 0 || after.openCursors != 0 || after.begins != after.ends {
					t.Fatal("foreign input resources outlived rollback/progress", after)
				}
			}
		}
		exercise(c, false)
		if !b.PG {
			// Preserve the registered-provider native control boundary. Remote SQL
			// rows remain external and are checked independently after every round.
			var snapshot bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
					t.Fatal(err)
				}
				func() {
					fresh, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer fresh.Close()
					exercise(fresh, false)
				}()
			}
		}
		fresh, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		exercise(fresh, true)
	})
}
