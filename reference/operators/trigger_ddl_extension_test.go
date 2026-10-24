package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// The table automatic dependency identifies trigger payloads without requiring
// pg_trigger or assuming any numeric object identity allocated by either backend.
func triggerDDLX(table, extension string, count int64) fixture.Step {
	return fixture.Select("SELECT count(*) AS n FROM pg_depend x JOIN pg_depend a ON a.classid=x.classid AND a.objid=x.objid AND a.objsubid=x.objsubid JOIN pg_extension e ON e.oid=x.refobjid WHERE x.classid=2620 AND x.objsubid=0 AND x.refclassid=3079 AND x.refobjsubid=0 AND x.deptype='x' AND a.refclassid=1259 AND a.refobjid='"+table+"'::regclass::oid AND a.deptype='a' AND e.extname='"+extension+"'", []string{"n"}, []string{"INT8"}, [][]any{{count}})
}

func TestReferenceTriggerDDLExtension(t *testing.T) {
	t.Run("ordinary", func(t *testing.T) {
		triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE EXTENSION unaccent", "CREATE EXTENSION fuzzystrmatch", "CREATE TABLE tr_other(id integer,v integer)",
				"CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()",
				"CREATE TRIGGER a BEFORE INSERT ON tr_other FOR EACH ROW EXECUTE FUNCTION tr_double()")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for range 2 {
				run(fixture.Prepared(fixture.Exec("ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION unaccent", 0)))
				run(triggerDDLX("tr_data", "unaccent", 1))
			}
			run(fixture.Exec("ALTER TRIGGER a ON tr_other DEPENDS ON EXTENSION unaccent", 0))
			run(fixture.Exec("ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION fuzzystrmatch", 0))
			for range 2 {
				run(fixture.Prepared(fixture.Exec("ALTER TRIGGER a ON tr_data NO DEPENDS ON EXTENSION unaccent", 0)))
				run(triggerDDLX("tr_data", "unaccent", 0))
				run(triggerDDLX("tr_data", "fuzzystrmatch", 1))
			}
			run(fixture.Exec("DROP EXTENSION unaccent RESTRICT", 0))
			run(triggerRows("INSERT INTO tr_data VALUES(1,5) RETURNING id,v", []any{int64(1), int64(10)}))
			run(fixture.Prepared(triggerRows("INSERT INTO tr_other VALUES(1,5) RETURNING id,v", []any{int64(1), int64(5)})))
			run(fixture.Exec("CREATE EXTENSION unaccent", 0))
			run(triggerDDLX("tr_other", "unaccent", 0))
			run(triggerRows("INSERT INTO tr_other VALUES(2,5) RETURNING id,v", []any{int64(2), int64(5)}))
			run(fixture.Exec("DROP TRIGGER a ON tr_data", 0))
			run(fixture.Select("SELECT extname FROM pg_extension WHERE extname IN ('unaccent','fuzzystrmatch') ORDER BY extname", []string{"extname"}, []string{"NAME"}, [][]any{{"fuzzystrmatch"}, {"unaccent"}}))
			userOperatorExec(t, p, b, c, "DROP EXTENSION unaccent", "DROP EXTENSION fuzzystrmatch")
		})
	})
	t.Run("normal-when-provider", func(t *testing.T) {
		triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE EXTENSION unaccent", "CREATE FUNCTION tr_allowed(integer) RETURNS boolean LANGUAGE SQL AS 'SELECT $1>0'", "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW WHEN(tr_allowed(NEW.v)) EXECUTE FUNCTION tr_double()", "ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION unaccent", "ALTER TRIGGER a ON tr_data NO DEPENDS ON EXTENSION unaccent")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Error("DROP FUNCTION tr_allowed(integer) RESTRICT", "2BP01"))
			run(fixture.Exec("DROP EXTENSION unaccent RESTRICT", 0))
			run(triggerRows("INSERT INTO tr_data VALUES(1,5) RETURNING id,v", []any{int64(1), int64(10)}))
			run(fixture.Exec("DROP FUNCTION tr_allowed(integer) CASCADE", 0))
			run(triggerRows("INSERT INTO tr_data VALUES(2,5) RETURNING id,v", []any{int64(2), int64(5)}))
		})
	})
	for _, mode := range []string{"RESTRICT", "CASCADE"} {
		t.Run("clone-only-"+mode, func(t *testing.T) {
			triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				triggerDDLPartitionSeed(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE EXTENSION unaccent", 0))
				run(fixture.Exec("ALTER TRIGGER a ON tr_leaf DEPENDS ON EXTENSION unaccent", 0))
				run(triggerDDLX("tr_parent", "unaccent", 0))
				run(triggerDDLX("tr_leaf", "unaccent", 1))
				want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop trigger a on table tr_leaf because trigger a on table tr_mid requires it")
				want.Hint = "You can drop trigger a on table tr_mid instead."
				run(fixture.ErrorDiagnostic("DROP EXTENSION unaccent "+mode, want))
				run(triggerDDLX("tr_leaf", "unaccent", 1))
				run(triggerRows("INSERT INTO tr_parent VALUES(1,5) RETURNING id,v", []any{int64(1), int64(8)}))
				run(fixture.Exec("ALTER TRIGGER a ON tr_leaf NO DEPENDS ON EXTENSION unaccent", 0))
				run(fixture.Exec("DROP EXTENSION unaccent "+mode, 0))
				run(triggerRows("INSERT INTO tr_parent VALUES(2,5) RETURNING id,v", []any{int64(2), int64(8)}))
			})
		})
	}
	t.Run("parent-retirement", func(t *testing.T) {
		triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerDDLPartitionSeed(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			userOperatorExec(t, p, b, c, "CREATE EXTENSION unaccent", "ALTER TRIGGER a ON tr_parent DEPENDS ON EXTENSION unaccent")
			run(triggerDDLX("tr_leaf", "unaccent", 0))
			run(fixture.Exec("DROP EXTENSION unaccent RESTRICT", 0))
			run(triggerRows("INSERT INTO tr_leaf VALUES(1,5) RETURNING id,v", []any{int64(1), int64(5)}))
			run(fixture.Exec("DROP FUNCTION tr_add() RESTRICT", 0))
		})
	})
}
