package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// SELECT CTE producers have their own invocation state. Modifying CTEs also
// retain actual child bindings, distinct from the main command's source leaves.
func TestReferenceRoutineSQLBodiesCTESourceCompaction(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "dcr_cte")
	sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE SCHEMA dcr_cte",
			"CREATE TABLE dcr_cte.source(unused integer,v integer,tail integer)",
			"CREATE TABLE dcr_cte.target(id integer,v integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dcr_cte.source VALUES(99,7,9)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dcr_cte.target VALUES(1,0)", 1))
		cases := []struct {
			name, body string
			insert     bool
		}{
			{"main_with_select", "WITH s AS MATERIALIZED(SELECT v FROM dcr_cte.source) UPDATE dcr_cte.target SET v=(SELECT v FROM s) WHERE id=1 RETURNING v", false},
			{"mixed_select", "WITH s AS MATERIALIZED(SELECT v FROM dcr_cte.source),m AS(UPDATE dcr_cte.target SET v=(SELECT v FROM s) WHERE id=1 RETURNING v) SELECT v FROM s", false},
			{"child_update", "WITH m AS(UPDATE dcr_cte.target t SET v=s.v FROM dcr_cte.source s WHERE t.id=1 RETURNING t.v) SELECT v FROM m", false},
			{"main_update_values", "WITH s AS MATERIALIZED(SELECT v FROM dcr_cte.source) UPDATE dcr_cte.target t SET v=s.v FROM s CROSS JOIN(VALUES(1))x(n) WHERE t.id=x.n RETURNING t.v", false},
			{"child_update_values", "WITH s AS MATERIALIZED(SELECT v FROM dcr_cte.source),m AS(UPDATE dcr_cte.target t SET v=s.v FROM s CROSS JOIN(VALUES(1))x(n) WHERE t.id=x.n RETURNING t.v) SELECT v FROM m", false},
			{"child_insert", "WITH m AS(INSERT INTO dcr_cte.target(id,v) SELECT 2,v FROM dcr_cte.source RETURNING v) SELECT v FROM m", true},
		}
		for _, tc := range cases {
			if !t.Run("create/"+tc.name, func(t *testing.T) {
				routineExec(t, p, b, c, "CREATE FUNCTION dcr_cte."+tc.name+"() RETURNS integer LANGUAGE SQL BEGIN ATOMIC "+tc.body+"; END")
			}) {
				t.FailNow()
			}
		}
		exercise := func(conn *sql.Conn, backend fixture.Backend, commit bool) {
			t.Helper()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			exec := func(q string) { run(fixture.Exec(q, 0)) }
			var sourceID, targetID int64
			identitySQL := "SELECT 'dcr_cte.source'::regclass::oid::bigint AS source_oid,'dcr_cte.target'::regclass::oid::bigint AS target_oid"
			if err := conn.QueryRowContext(p.Ctx, identitySQL).Scan(&sourceID, &targetID); err != nil {
				t.Fatal(err)
			}
			ids := make([]int64, len(cases))
			held := make([]fixture.HeldStatement, len(cases))
			for i, tc := range cases {
				if err := conn.QueryRowContext(p.Ctx, "SELECT 'dcr_cte."+tc.name+"()'::regprocedure::oid::bigint").Scan(&ids[i]); err != nil {
					t.Fatal(err)
				}
				q := "SELECT dcr_cte." + tc.name + "() AS v"
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("prepare before CTE source compaction", err)
				}
				defer stmt.Close()
				held[i] = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			}
			state := func(compacted bool) {
				t.Helper()
				run(fixture.Select(identitySQL, []string{"source_oid", "target_oid"}, []string{"INT8", "INT8"}, [][]any{{sourceID, targetID}}))
				for i, tc := range cases {
					run(fixture.Select("SELECT 'dcr_cte."+tc.name+"()'::regprocedure::oid::bigint AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{ids[i]}}))
					run(fixture.Select("SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='dcr_cte."+tc.name+"()'::regprocedure AND refclassid='pg_class'::regclass AND refobjid='dcr_cte.source'::regclass ORDER BY refobjsubid,deptype",
						[]string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"},
						[][]any{{int64(1255), ids[i], int64(0), int64(1259), sourceID, int64(2), "n"}}))
				}
				attrs := [][]any{{"unused", int64(1)}, {"v", int64(2)}, {"tail", int64(3)}}
				if compacted {
					attrs = [][]any{{"v", int64(2)}, {"tail", int64(3)}}
					run(fixture.Select("SELECT * FROM dcr_cte.source", []string{"v", "tail"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
				} else {
					run(fixture.Select("SELECT * FROM dcr_cte.source", []string{"unused", "v", "tail"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(99), int64(7), int64(9)}}))
				}
				run(fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='dcr_cte.source'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, attrs))
				run(fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='dcr_cte.target'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, [][]any{{"id", int64(1)}, {"v", int64(2)}}))
			}
			reset := func() {
				exec("TRUNCATE dcr_cte.target")
				run(fixture.Exec("INSERT INTO dcr_cte.target VALUES(1,0)", 1))
			}
			invoke := func(i int, x fixture.Executor, route string) {
				t.Helper()
				if !t.Run(cases[i].name+"/"+route, func(t *testing.T) {
					reset()
					fixture.RunStep(t, p.Ctx, x, backend.Recorder, fixture.Select(held[i].SQL, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					want := [][]any{{int64(1), int64(7)}}
					if cases[i].insert {
						want = [][]any{{int64(1), int64(0)}, {int64(2), int64(7)}}
					}
					fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT id,v FROM dcr_cte.target ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
				}) {
					t.FailNow()
				}
			}
			all := func() {
				for i := range cases {
					invoke(i, held[i], "held-first")
					invoke(i, held[i], "held-second")
					invoke(i, conn, "direct")
				}
			}
			restrict := func(inTransaction bool) {
				var details []string
				for _, tc := range cases {
					details = append(details, "function dcr_cte."+tc.name+"() depends on column v of table dcr_cte.source")
				}
				d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop column v of table dcr_cte.source because other objects depend on it")
				d.Detail, d.Hint = strings.Join(details, "\n"), "Use DROP ... CASCADE to drop the dependent objects too."
				for _, prepared := range []bool{false, true} {
					if inTransaction {
						exec("SAVEPOINT used_source")
					}
					s := fixture.ErrorDiagnostic("ALTER TABLE dcr_cte.source DROP v RESTRICT", d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					if inTransaction {
						exec("ROLLBACK TO used_source")
						exec("RELEASE used_source")
					}
					state(true)
					run(fixture.Select("SELECT id,v FROM dcr_cte.target ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}, {int64(2), int64(7)}}))
				}
			}
			state(false)
			all()
			reset()
			exec("BEGIN")
			open := true
			defer func() {
				if open {
					_, _ = conn.ExecContext(p.Ctx, "ROLLBACK")
				}
			}()
			exec("SAVEPOINT before_drop")
			exec("ALTER TABLE dcr_cte.source DROP unused RESTRICT")
			state(true)
			if commit {
				exec("COMMIT")
				open = false
				all()
				restrict(false)
				return
			}
			all()
			restrict(true)
			exec("ROLLBACK TO before_drop")
			state(false)
			run(fixture.Select("SELECT id,v FROM dcr_cte.target", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}}))
			exec("ROLLBACK")
			open = false
			state(false)
		}
		check := func(conn *sql.Conn, backend fixture.Backend) { exercise(conn, backend, false) }
		finish := func(conn *sql.Conn, backend fixture.Backend) { exercise(conn, backend, true) }
		sqlBodiesReplayTwice(t, p, b, c, "dcr_cte", check, finish)
	})
}
