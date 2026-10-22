package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineSQLBodiesMergeCaptures(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "body_merge")
	sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE SCHEMA body_merge",
			"CREATE TABLE body_merge.target(id integer,v bigint)",
			"CREATE TABLE body_merge.source(id integer,v integer)",
			"CREATE FUNCTION body_merge.on_key(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION body_merge.when_key(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION body_merge.action_key(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+10$$",
			"CREATE FUNCTION body_merge.result_key(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+1000$$",
			"CREATE FUNCTION body_merge.dormant(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION body_merge.run() RETURNS bigint BEGIN ATOMIC MERGE INTO body_merge.target t USING body_merge.source s ON body_merge.on_key(t.id)=s.id WHEN MATCHED AND false THEN UPDATE SET v=body_merge.dormant(s.v) WHEN MATCHED AND t.id<0 THEN DELETE WHEN MATCHED AND body_merge.when_key(s.id)=1 THEN UPDATE SET v=body_merge.action_key(s.v) WHEN MATCHED THEN DO NOTHING WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,body_merge.action_key(s.v)) RETURNING body_merge.result_key(1); END",
			"CREATE FUNCTION body_merge.cleanup() RETURNS void BEGIN ATOMIC MERGE INTO body_merge.target t USING body_merge.source s ON t.id=s.id WHEN MATCHED THEN DO NOTHING WHEN NOT MATCHED BY SOURCE AND t.id=4 THEN DELETE WHEN NOT MATCHED BY SOURCE THEN DO NOTHING; END",
			"ALTER TABLE body_merge.target RENAME TO saved_target",
			"ALTER TABLE body_merge.source RENAME TO saved_source",
			"CREATE TABLE body_merge.target(id integer,v bigint)",
			"CREATE TABLE body_merge.source(id integer,v integer)")
		for _, name := range []string{"on_key", "when_key", "action_key", "result_key", "dormant"} {
			routineExec(t, p, b, c, "CREATE FUNCTION body_merge."+name+"(integer) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+10000$$")
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO body_merge.saved_source VALUES(1,5),(2,99),(3,7)", 3))
		check := func(conn *sql.Conn, backend fixture.Backend) {
			t.Helper()
			// Re-seed so each replay checks every supported action, not merely an
			// already-converged target or an unspecified first RETURNING row.
			routineExec(t, p, backend, conn, "TRUNCATE body_merge.saved_target")
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("INSERT INTO body_merge.saved_target VALUES(1,10),(2,20),(4,40),(5,50)", 4))
			routineValue(t, p, backend, conn, "SELECT body_merge.run() AS v", "v", "INT8", int64(1001))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT body_merge.cleanup() IS NULL AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT id,v FROM body_merge.saved_target ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(15)}, {int64(2), int64(20)}, {int64(3), int64(17)}, {int64(5), int64(50)}}))
			routineValue(t, p, backend, conn, "SELECT body_merge.action_key(5) AS v", "v", "INT8", int64(10005))
			for _, object := range []string{"TABLE body_merge.saved_source", "TABLE body_merge.saved_target", "FUNCTION body_merge.dormant(bigint)"} {
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Error("DROP "+object, "2BP01"))
			}
			// All five selected callees, including the never-taken UPDATE arm,
			// must be hard dependencies. The later integer overloads are absent.
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select(
				"SELECT DISTINCT refobjid::regprocedure::text AS callee,deptype FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='body_merge.run()'::regprocedure AND refclassid='pg_proc'::regclass ORDER BY callee,deptype",
				[]string{"callee", "deptype"}, []string{"TEXT", "CHAR"}, [][]any{{"body_merge.action_key(bigint)", "n"}, {"body_merge.dormant(bigint)", "n"}, {"body_merge.on_key(bigint)", "n"}, {"body_merge.result_key(bigint)", "n"}, {"body_merge.when_key(bigint)", "n"}}))
		}
		finish := func(conn *sql.Conn, backend fixture.Backend) {
			t.Helper()
			// Preserve the creation-before-overload captures through both native
			// replacements. Destructive CASCADE runs only after the final native
			// round, and on each independent emitted PostgreSQL replay.
			routineExec(t, p, backend, conn, "DROP FUNCTION body_merge.dormant(bigint) CASCADE")
			s := fixture.Error("SELECT body_merge.run()", "42883")
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT body_merge.cleanup() IS NULL AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
		}
		sqlBodiesReplayTwice(t, p, b, c, "body_merge", check, finish)
	})
}
