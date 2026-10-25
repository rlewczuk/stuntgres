package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func tableoidDependencies(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, function, table string, wantEdge bool) {
	t.Helper()
	var want [][]any
	if wantEdge {
		want = [][]any{{tableoidIdentity(t, p, c, table), int64(-6), "n"}}
	}
	q := "SELECT d.refobjid::bigint AS relation,d.refobjsubid,d.deptype FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid='" + function + "()'::regprocedure AND d.refclassid='pg_class'::regclass ORDER BY d.refobjid,d.refobjsubid,d.deptype"
	tableoidRoutes(t, p, b, c, fixture.Select(q, []string{"relation", "refobjsubid", "deptype"}, []string{"INT8", "INT4", "CHAR"}, want))
}

func TestReferenceQueryTableOIDDependencies(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE TABLE toi_dep(a INT)", "CREATE TABLE toi_dep_child() INHERITS(toi_dep)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO toi_dep_child VALUES(1)", 1))
		routineExec(t, p, b, c,
			"CREATE FUNCTION toi_hard() RETURNS oid LANGUAGE SQL BEGIN ATOMIC SELECT q.tableoid FROM toi_dep q; END",
			"CREATE FUNCTION toi_soft() RETURNS oid LANGUAGE SQL AS $$SELECT q.tableoid FROM toi_dep q$$")
		check := func(table, child string) {
			oid := tableoidCell(b, tableoidIdentity(t, p, c, child))
			tableoidRoutes(t, p, b, c, fixture.Select("SELECT toi_hard() AS physical", []string{"physical"}, []string{"OID"}, [][]any{{oid}}))
			tableoidDependencies(t, p, b, c, "toi_hard", table, true)
			tableoidDependencies(t, p, b, c, "toi_soft", table, false)
		}
		check("toi_dep", "toi_dep_child")
		tableoidRoutes(t, p, b, c, fixture.Select("SELECT toi_soft() AS physical", []string{"physical"}, []string{"OID"}, [][]any{{tableoidCell(b, tableoidIdentity(t, p, c, "toi_dep_child"))}}))
		q := "DROP TABLE toi_dep RESTRICT"
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "drop_direct", true: "drop_prepared"}[prepared], func(t *testing.T) {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table toi_dep because other objects depend on it", Detail: "table toi_dep_child depends on table toi_dep\nfunction toi_hard() depends on table toi_dep", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
		routineExec(t, p, b, c, "ALTER TABLE toi_dep RENAME TO toi_dep_saved", "CREATE TABLE toi_dep(a INT)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO toi_dep VALUES(9)", 1))
		check("toi_dep_saved", "toi_dep_child")
		tableoidRoutes(t, p, b, c, fixture.Select("SELECT toi_soft() AS physical", []string{"physical"}, []string{"OID"}, [][]any{{tableoidCell(b, tableoidIdentity(t, p, c, "toi_dep"))}}))
		// Quoted source does not prevent dropping its newly resolved relation.
		routineExec(t, p, b, c, "DROP TABLE toi_dep", "CREATE TABLE toi_dep(a INT)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO toi_dep VALUES(10)", 1))
		tableoidRoutes(t, p, b, c, fixture.Select("SELECT toi_soft() AS physical", []string{"physical"}, []string{"OID"}, [][]any{{tableoidCell(b, tableoidIdentity(t, p, c, "toi_dep"))}}))
	})
}
