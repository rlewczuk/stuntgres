package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertSelectColumnGrantOracle(t *testing.T) {
	const writer = "insert_select_column_writer"
	fixture.RoleReferenceCleanup(t, []string{writer})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		if backend.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		run(fixture.Exec("CREATE ROLE "+writer, 0))
		run(fixture.Exec("CREATE TABLE s(i INT,v INT,b BOOL)", 0))
		run(fixture.Exec("INSERT INTO s VALUES(1,2,TRUE)", 1))
		run(fixture.Exec("CREATE TABLE t(i INT)", 0))
		for _, q := range []string{"GRANT INSERT(i) ON t TO " + writer, "GRANT SELECT(i,b) ON s TO " + writer} {
			if _, err := backend.DB.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("SET SESSION AUTHORIZATION "+writer, 0))
		failure := fixture.ImplicitError("INSERT INTO t SELECT i FROM s WHERE b ORDER BY v", fixture.Diagnostic{Code: "42501", Message: "permission denied for table s"})
		failure.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, failure)
		if _, err := backend.DB.ExecContext(p.Ctx, "GRANT SELECT(v) ON s TO "+writer); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO t SELECT i FROM s WHERE b ORDER BY v", 1)))
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}))
	}
}
