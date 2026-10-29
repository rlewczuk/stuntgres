package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceVisibilityExecutionPathAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"vex_reader"})
	executionVisibilityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		objects := executionVisibilityObjects()
		ids := make([]int64, len(objects))
		for i, object := range objects {
			if err := conn.QueryRowContext(p.Ctx, "SELECT ("+object.identity+")::bigint").Scan(&ids[i]); err != nil || ids[i] == 0 { t.Fatal(ids[i], err) }
		}
		held := make([]*sql.Stmt, 4)
		for i, fn := range executionVisibilityNames {
			stmt, err := conn.PrepareContext(p.Ctx, "SELECT pg_catalog."+fn+"($1)")
			if err != nil { t.Fatal(err) }
			held[i] = stmt
			defer stmt.Close()
		}
		check := func(first, second, alternate bool) {
			for kind, fn := range executionVisibilityNames {
				for i, want := range []bool{first, second, alternate} {
					s := executionVisibilityDecision(fn, "$1", want); s.Args = []any{ids[kind+4*i]}; s.CheckMetadata = fixture.VisibilityBoolMetadata
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held[kind], SQL: s.Query}, b.Recorder, s)
				}
			}
		}
		run(fixture.Exec("SET search_path=vex_missing,vex_a,vex_b", 0)); check(true, false, true)
		run(fixture.Exec("SET search_path=vex_b,vex_missing,vex_a", 0)); check(false, true, true)
		run(fixture.Exec("SET search_path=public", 0)); check(false, false, false)
		for _, q := range []string{
			"CREATE ROLE vex_reader", "GRANT USAGE ON SCHEMA vex_a,vex_b TO vex_reader",
			"REVOKE EXECUTE ON FUNCTION vex_a.vis_fn(integer) FROM PUBLIC",
			"REVOKE EXECUTE ON FUNCTION vex_a.vis_fn(integer) FROM vex_reader",
			"SET search_path=vex_a,vex_b", "SET ROLE vex_reader",
		} { run(fixture.Exec(q, 0)) }
		check(true, false, true)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE USAGE ON SCHEMA vex_a FROM vex_reader", 0))
		run(fixture.Exec("SET ROLE vex_reader", 0)); check(false, true, true)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA vex_a TO vex_reader", 0))
		run(fixture.Exec("SET ROLE vex_reader", 0)); check(true, false, true)
		run(fixture.Exec("RESET ROLE", 0))
		// An inquiry must not create a session's temporary namespace.
		run(fixture.Select("SELECT pg_my_temp_schema()::bigint", []string{"pg_my_temp_schema"}, []string{"INT8"}, [][]any{{int64(0)}}))
		check(true, false, true)
		run(fixture.Select("SELECT pg_my_temp_schema()::bigint", []string{"pg_my_temp_schema"}, []string{"INT8"}, [][]any{{int64(0)}}))
		for _, q := range []string{
			"CREATE TEMP TABLE vex_temp_anchor(id integer)",
			"CREATE FUNCTION pg_temp.vis_fn(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
			"CREATE OPERATOR pg_temp.## (LEFTARG=integer, RIGHTARG=integer, FUNCTION=vex_a.op)",
			"CREATE OPERATOR CLASS pg_temp.vis_class FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)",
			"CREATE OPERATOR FAMILY pg_temp.vis_family USING btree",
			"SET search_path=pg_temp,vex_a,vex_b",
		} { run(fixture.Exec(q, 0)) }
		check(true, false, true)
		for i, object := range []executionVisibilityObject{
			{executionVisibilityNames[0], "(SELECT oid FROM pg_proc WHERE pronamespace=pg_my_temp_schema() AND proname='vis_fn' AND proargtypes::text='23')", ""},
			{executionVisibilityNames[1], "(SELECT oid FROM pg_operator WHERE oprnamespace=pg_my_temp_schema() AND oprname='##' AND oprleft=23 AND oprright=23)", ""},
			{executionVisibilityNames[2], "(SELECT oid FROM pg_opclass WHERE opcnamespace=pg_my_temp_schema() AND opcname='vis_class' AND opcmethod=403)", ""},
			{executionVisibilityNames[3], "(SELECT oid FROM pg_opfamily WHERE opfnamespace=pg_my_temp_schema() AND opfname='vis_family' AND opfmethod=403)", ""},
		} {
			run(executionVisibilityDecision(executionVisibilityNames[i], object.identity, false))
		}
		for _, q := range []string{"DROP OPERATOR CLASS pg_temp.vis_class USING btree", "DROP OPERATOR FAMILY pg_temp.vis_family USING btree", "DROP OPERATOR pg_temp.##(integer,integer)", "DROP FUNCTION pg_temp.vis_fn(integer)", "DROP TABLE pg_temp.vex_temp_anchor", "RESET search_path"} { run(fixture.Exec(q, 0)) }
	})
}
