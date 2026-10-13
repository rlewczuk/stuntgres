package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeNothingPrivileges(t *testing.T) {
	mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		role := "merge_nothing_reader"
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		for _, q := range []string{"CREATE USER " + role, "CREATE TABLE t(id INT,v INT)", "CREATE TABLE source(id INT)", "GRANT SELECT ON source TO " + role} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		run(fixture.Exec("INSERT INTO source VALUES(1)", 1))
		deny := func(q string) {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
			s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
			run(s)
		}
		queries := []string{
			"MERGE INTO t USING (VALUES(1)) s(id) ON TRUE WHEN MATCHED THEN DO NOTHING RETURNING merge_action(),1",
			"MERGE INTO t USING (VALUES(1)) s(id) ON TRUE WHEN MATCHED AND FALSE THEN DO NOTHING RETURNING merge_action(),1",
			"MERGE INTO t USING (SELECT id FROM source WHERE FALSE) s ON TRUE WHEN MATCHED THEN DO NOTHING RETURNING merge_action(),1",
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		for _, q := range queries {
			deny(q)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(v) ON t TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		for _, q := range queries {
			run(insertReturningBag(q, []string{"merge_action", "?column?"}, []string{"TEXT", "INT4"}, [][]any{}, prepared))
		}
		deny("MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DO NOTHING RETURNING 1")
		for _, arm := range []string{"WHEN MATCHED AND FALSE THEN UPDATE SET v=99 WHEN MATCHED THEN DO NOTHING", "WHEN MATCHED AND FALSE THEN DELETE WHEN MATCHED THEN DO NOTHING", "WHEN MATCHED THEN DO NOTHING WHEN NOT MATCHED AND FALSE THEN INSERT VALUES(s.id,99)"} {
			deny("MERGE INTO t USING (VALUES(1)) s(id) ON TRUE " + arm + " RETURNING 1")
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON t TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		run(insertReturningBag("MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED AND (SELECT t.v)=10 THEN DO NOTHING RETURNING t.id", []string{"id"}, []string{"INT4"}, [][]any{}, prepared))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
	}, func(t *testing.T) { fixture.RoleReferenceCleanup(t, []string{"merge_nothing_reader"}) })
}
func TestReferenceMergeNothingHeldACL(t *testing.T) {
	mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		role := "merge_nothing_held"
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		for _, q := range []string{"CREATE USER " + role, "CREATE TABLE t(id INT,v INT)", "GRANT SELECT(v) ON t TO " + role} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		q := "MERGE INTO t USING (VALUES(1)) s(id) ON TRUE WHEN MATCHED THEN DO NOTHING RETURNING merge_action(),1"
		held, e := a.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer held.Close()
		x := fixture.HeldStatement{Executor: a, Statement: held, SQL: q}
		ok := fixture.Select(q, []string{"merge_action", "?column?"}, []string{"TEXT", "INT4"}, [][]any{})
		fixture.RunStep(t, p.Ctx, x, b.Recorder, ok)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT(v) ON t FROM "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		deny := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
		deny.SelectRows, deny.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, x, b.Recorder, deny)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(v) ON t TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		fixture.RunStep(t, p.Ctx, x, b.Recorder, ok)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
	}, func(t *testing.T) { fixture.RoleReferenceCleanup(t, []string{"merge_nothing_held"}) })
}
