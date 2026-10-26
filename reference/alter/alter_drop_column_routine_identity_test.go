package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterDropColumnRoutineIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rtu_moved")
	dcrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		dcrExec(t, p, b, c,
			"CREATE SCHEMA rtu_moved",
			"CREATE TABLE rtu_life_t(a integer,b integer)",
			"CREATE TABLE rtu_life_q(a integer)",
			"CREATE TABLE rtu_life_unrelated(a integer)",
			"CREATE FUNCTION rtu_life_use() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT a FROM rtu_life_t; END",
			"CREATE FUNCTION rtu_life_keep() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT a FROM rtu_life_q; END",
			"CREATE FUNCTION rtu_life_quoted() RETURNS integer LANGUAGE SQL AS 'SELECT a FROM rtu_life_t'")
		dcrRun(t, p, b, c, fixture.Exec("INSERT INTO rtu_life_t VALUES(7,8)", 1))
		dcrRun(t, p, b, c, fixture.Exec("INSERT INTO rtu_life_q VALUES(17)", 1))
		identity := dcrRows(t, p, c, "SELECT 'rtu_life_use()'::regprocedure::oid::bigint,'rtu_life_t'::regclass::oid::bigint")
		dcrExec(t, p, b, c, "ALTER TABLE rtu_life_t DROP b", "ALTER TABLE rtu_life_t RENAME a TO renamed", "ALTER TABLE rtu_life_t RENAME TO rtu_life_renamed", "ALTER TABLE rtu_life_renamed SET SCHEMA rtu_moved", "ALTER FUNCTION rtu_life_use() RENAME TO rtu_life_new", "ALTER FUNCTION rtu_life_new() SET SCHEMA rtu_moved", "DROP TABLE rtu_life_unrelated")
		if got := dcrRows(t, p, c, "SELECT 'rtu_moved.rtu_life_new()'::regprocedure::oid::bigint,'rtu_moved.rtu_life_renamed'::regclass::oid::bigint"); !reflect.DeepEqual(got, identity) {
			t.Fatal("rename/move changed stable identities", got, identity)
		}
		dcrAttributeEdges(t, p, b, c, "rtu_moved.rtu_life_new()", "rtu_moved.rtu_life_renamed", 1)
		want := dcrRestrict("rtu_moved.rtu_life_renamed", "renamed", "function rtu_moved.rtu_life_new() depends on column renamed of table rtu_moved.rtu_life_renamed")
		for _, held := range []bool{false, true} {
			dcrError(t, p, b, c, "ALTER TABLE rtu_moved.rtu_life_renamed DROP renamed RESTRICT", want, held)
			dcrValue(t, p, b, c, "SELECT rtu_moved.rtu_life_new() AS v", int64(7))
			dcrNotices(t, notices)
		}
		// A same-spelling replacement must not acquire the original's edges.
		dcrExec(t, p, b, c, "CREATE TABLE rtu_life_t(a integer,b integer)", "ALTER TABLE rtu_life_t DROP a")
		dcrValue(t, p, b, c, "SELECT rtu_moved.rtu_life_new() AS v", int64(7))
		dcrExec(t, p, b, c, "DROP FUNCTION rtu_moved.rtu_life_new()", "CREATE FUNCTION rtu_life_use() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT a FROM rtu_life_q; END")
		newID := dcrRows(t, p, c, "SELECT 'rtu_life_use()'::regprocedure::oid::bigint")
		if newID[0][0] == identity[0][0] {
			t.Fatal("recreated routine reused retired identity")
		}
		dcrExec(t, p, b, c, "DROP TABLE rtu_moved.rtu_life_renamed", "DROP FUNCTION rtu_life_use()", "ALTER TABLE rtu_life_q DROP a CASCADE")
		dcrNotices(t, notices, "function rtu_life_keep()")
		dcrRun(t, p, b, c, fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname LIKE 'rtu_life_%' ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{{"rtu_life_quoted"}}))
	})
}
