package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeReturningPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"merge_ret_reader", "merge_ret_held"})
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		reader := "merge_ret_reader"
		if prepared {
			reader = "merge_ret_held"
		}
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		for _, q := range []string{"CREATE USER " + reader, "CREATE TABLE t(id INT,v INT,secret TEXT)", "CREATE TABLE source(sid INT,source_secret TEXT)", "CREATE TABLE labels(label TEXT)", "GRANT INSERT,UPDATE,DELETE ON t TO " + reader, "GRANT SELECT(id,v) ON t TO " + reader, "GRANT SELECT(sid) ON source TO " + reader} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO t VALUES(1,10,'hidden')", 1))
		run(fixture.Exec("INSERT INTO source VALUES(1,'source hidden')", 1))
		run(fixture.Exec("INSERT INTO labels VALUES('label')", 1))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		base := "MERGE INTO t USING source s ON t.id=s.sid WHEN MATCHED THEN UPDATE SET v=t.v+1 RETURNING "
		s := fixture.Select(base+"merge_action(),1", []string{"merge_action", "?column?"}, []string{"TEXT", "INT4"}, [][]any{{"UPDATE", int64(1)}})
		s.Prepared = prepared
		run(s)
		deny := func(q, table string) {
			t.Helper()
			d := fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + table}
			s := fixture.ImplicitError(q, d)
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
		}
		for _, out := range []string{"secret", "old.secret", "new.secret", "t.*", "*", "old", "new", "t", "CASE WHEN FALSE THEN secret ELSE 'safe' END"} {
			deny(base+out, "t")
			deny("MERGE INTO t USING (SELECT sid FROM source WHERE FALSE) s ON t.id=s.sid WHEN MATCHED THEN UPDATE SET v=99 RETURNING "+out, "t")
			deny("MERGE INTO t USING source s ON t.id=s.sid WHEN MATCHED AND FALSE THEN DELETE RETURNING "+out, "t")
		}
		deny(base+"(SELECT label FROM labels)", "labels")
		for _, out := range []string{"s.source_secret", "s.*", "s", "CASE WHEN FALSE THEN s.source_secret ELSE 'safe' END"} {
			deny(base+out, "source")
		}
		deny("MERGE INTO t USING source s ON FALSE WHEN NOT MATCHED BY SOURCE THEN DELETE RETURNING s.source_secret", "source")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT * FROM t", []string{"id", "v", "secret"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(11), "hidden"}}))
		run(fixture.Exec("GRANT SELECT(secret) ON t TO "+reader, 0))
		run(fixture.Exec("GRANT SELECT ON labels TO "+reader, 0))
		run(fixture.Exec("GRANT SELECT(source_secret) ON source TO "+reader, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		s = fixture.Select("MERGE INTO t USING source s ON t.id=s.sid WHEN MATCHED THEN UPDATE SET v=t.v RETURNING s.source_secret", []string{"source_secret"}, []string{"TEXT"}, [][]any{{"source hidden"}})
		s.Prepared = prepared
		run(s)
		q := base + "secret,(SELECT label FROM labels) AS label"
		held, e := a.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer held.Close()
		x := fixture.HeldStatement{Executor: a, Statement: held, SQL: q}
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q, []string{"secret", "label"}, []string{"TEXT", "TEXT"}, [][]any{{"hidden", "label"}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT(secret) ON t FROM "+reader, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		denied := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
		denied.SelectRows = true
		denied.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, x, b.Recorder, denied)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(12)}}))
		run(fixture.Exec("GRANT SELECT(secret) ON t TO "+reader, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q, []string{"secret", "label"}, []string{"TEXT", "TEXT"}, [][]any{{"hidden", "label"}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(13)}}))
		// Every arm requires its action privilege even when its condition is false.
		run(fixture.Exec("REVOKE DELETE ON t FROM "+reader, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		deny("MERGE INTO t USING source s ON t.id=s.sid WHEN MATCHED AND FALSE THEN DELETE WHEN MATCHED THEN UPDATE SET v=99 RETURNING 1", "t")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("CREATE TABLE empty()", 0))
		run(fixture.Exec("INSERT INTO empty DEFAULT VALUES", 1))
		run(fixture.Exec("GRANT DELETE ON empty TO "+reader, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		deny("MERGE INTO empty USING (VALUES(1)) s(x) ON TRUE WHEN MATCHED THEN DELETE RETURNING old", "empty")
		s = fixture.Select("MERGE INTO empty USING (VALUES(1)) s(x) ON TRUE WHEN MATCHED THEN DELETE RETURNING merge_action()", []string{"merge_action"}, []string{"TEXT"}, [][]any{{"DELETE"}})
		s.Prepared = prepared
		run(s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
