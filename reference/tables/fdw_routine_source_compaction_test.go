package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Each destination is genuinely foreign: native uses the registered SQL writer,
// PostgreSQL its real loopback postgres_fdw. The local source alone compacts.
func TestReferenceFDWRoutineSourceCompaction(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		run(fixture.Exec("CREATE TABLE dcr_foreign_source(unused integer,v integer,tail integer)", 0))
		run(fixture.Exec("INSERT INTO dcr_foreign_source VALUES(99,7,9)", 1))
		cases := []struct{ name, command, operation string }{
			{"dcr_foreign_insert_select", "INSERT INTO fdw_data(id,label,flag) SELECT v,'routine',true FROM dcr_foreign_source", "insert"},
			{"dcr_foreign_insert_values", "INSERT INTO fdw_data VALUES((SELECT v FROM dcr_foreign_source),'routine',true)", "insert"},
			{"dcr_foreign_update_from", "UPDATE fdw_data f SET id=s.v,label='routine',flag=true FROM dcr_foreign_source s WHERE f.id=1", "update"},
			{"dcr_foreign_update_scalar", "UPDATE fdw_data SET id=(SELECT v FROM dcr_foreign_source),label='routine',flag=true WHERE id=1", "update"},
			{"dcr_foreign_delete_using", "DELETE FROM fdw_data f USING dcr_foreign_source s WHERE f.id=s.v", "delete"},
			{"dcr_foreign_delete_scalar", "DELETE FROM fdw_data WHERE id=(SELECT v FROM dcr_foreign_source)", "delete"},
		}
		for _, tc := range cases {
			if !t.Run("create/"+tc.name, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE FUNCTION "+tc.name+"() RETURNS void LANGUAGE SQL BEGIN ATOMIC "+tc.command+"; END", 0))
			}) {
				t.FailNow()
			}
		}
		exercise := func(conn *sql.Conn, commit bool) {
			t.Helper()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			exec := func(q string) { run(fixture.Exec(q, 0)) }
			var sourceID, targetID int64
			identitySQL := "SELECT 'dcr_foreign_source'::regclass::oid::bigint AS source_oid,'fdw_data'::regclass::oid::bigint AS target_oid"
			if err := conn.QueryRowContext(p.Ctx, identitySQL).Scan(&sourceID, &targetID); err != nil {
				t.Fatal(err)
			}
			ids := make([]int64, len(cases))
			held := make([]fixture.HeldStatement, len(cases))
			for i, tc := range cases {
				if err := conn.QueryRowContext(p.Ctx, "SELECT '"+tc.name+"()'::regprocedure::oid::bigint").Scan(&ids[i]); err != nil {
					t.Fatal(err)
				}
				query := "SELECT " + tc.name + "() IS NULL AS done"
				statement, err := conn.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal("prepare before source compaction", err)
				}
				defer statement.Close()
				held[i] = fixture.HeldStatement{Executor: conn, Statement: statement, SQL: query}
			}
			state := func(compacted bool) {
				t.Helper()
				run(fixture.Select(identitySQL, []string{"source_oid", "target_oid"}, []string{"INT8", "INT8"}, [][]any{{sourceID, targetID}}))
				for i, tc := range cases {
					run(fixture.Select("SELECT '"+tc.name+"()'::regprocedure::oid::bigint AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{ids[i]}}))
					run(fixture.Select("SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='"+tc.name+"()'::regprocedure AND refclassid='pg_class'::regclass AND refobjid='dcr_foreign_source'::regclass ORDER BY refobjsubid,deptype",
						[]string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"},
						[][]any{{int64(1255), ids[i], int64(0), int64(1259), sourceID, int64(2), "n"}}))
				}
				attrs := [][]any{{"unused", int64(1)}, {"v", int64(2)}, {"tail", int64(3)}}
				if compacted {
					attrs = [][]any{{"v", int64(2)}, {"tail", int64(3)}}
					run(fixture.Select("SELECT * FROM dcr_foreign_source", []string{"v", "tail"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
				} else {
					run(fixture.Select("SELECT * FROM dcr_foreign_source", []string{"unused", "v", "tail"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(99), int64(7), int64(9)}}))
				}
				run(fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='dcr_foreign_source'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, attrs))
				run(fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='fdw_data'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, [][]any{{"id", int64(1)}, {"label", int64(2)}, {"flag", int64(3)}}))
			}
			clear := func(t *testing.T) {
				t.Helper()
				if _, err := conn.ExecContext(p.Ctx, "DELETE FROM fdw_data"); err != nil {
					t.Fatal("clear real destination", err)
				}
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id", nil))
			}
			invoke := func(i int, x fixture.Executor, external bool, route string) {
				t.Helper()
				if !t.Run(cases[i].name+"/"+route, func(t *testing.T) {
					clear(t)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					want := [][]any{{int64(7), "routine", true}}
					switch cases[i].operation {
					case "update":
						run(fixture.Exec("INSERT INTO fdw_data VALUES(1,'before',false)", 1))
					case "delete":
						run(fixture.Exec("INSERT INTO fdw_data VALUES(7,'remove',true),(9,'sentinel',false)", 2))
						want = [][]any{{int64(9), "sentinel", false}}
					}
					fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(held[i].SQL, []string{"done"}, []string{"BOOL"}, [][]any{{true}}))
					run(fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id", want))
					if external {
						fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", want))
					}
				}) {
					t.FailNow()
				}
			}
			all := func(external bool) {
				for i := range cases {
					invoke(i, held[i], external, "held-first")
					invoke(i, held[i], external, "held-second")
					invoke(i, conn, external, "direct")
				}
			}
			restrict := func(inTransaction bool) {
				t.Helper()
				var detail []string
				for _, tc := range cases {
					detail = append(detail, "function "+tc.name+"() depends on column v of table dcr_foreign_source")
				}
				d := fdwDiagnostic("2BP01", "cannot drop column v of table dcr_foreign_source because other objects depend on it")
				d.Detail, d.Hint = strings.Join(detail, "\n"), "Use DROP ... CASCADE to drop the dependent objects too."
				for _, prepared := range []bool{false, true} {
					if inTransaction {
						exec("SAVEPOINT used_source")
					}
					s := fixture.ErrorDiagnostic("ALTER TABLE dcr_foreign_source DROP v RESTRICT", d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					if inTransaction {
						exec("ROLLBACK TO used_source")
						exec("RELEASE used_source")
					}
					state(true)
					run(fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id", [][]any{{int64(9), "sentinel", false}}))
				}
			}
			state(false)
			all(true)
			clear(t)
			exec("BEGIN")
			open := true
			defer func() {
				if open {
					_, _ = conn.ExecContext(p.Ctx, "ROLLBACK")
				}
			}()
			exec("SAVEPOINT before_drop")
			exec("ALTER TABLE dcr_foreign_source DROP unused RESTRICT")
			state(true)
			if commit {
				exec("COMMIT")
				open = false
				all(true)
				restrict(false)
				return
			}
			all(false)
			restrict(true)
			exec("ROLLBACK TO before_drop")
			state(false)
			run(fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id", nil))
			exec("ROLLBACK")
			open = false
			state(false)
			fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", nil))
		}
		exercise(c, false)
		if !b.PG {
			// Native handler registrations survive replacement. Remote contents are
			// intentionally not dumped; every witness reseeds the real destination.
			// This native-handler stream is not rewritten into postgres_fdw SQL.
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
