package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeInsertAuthorization(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			if *fixture.Mode {
				fixture.RoleReferenceCleanup(t, []string{"merge_denied_actor"})
			}
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := fixture.MergeRun(t, p, b, prepared)
					run(fixture.Exec("CREATE USER merge_denied_actor", 0))
					run(fixture.Exec("CREATE TABLE t(id INT)", 0))
					run(fixture.Exec("CREATE TABLE s(id INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(99)", 1))
					if b.PG {
						run(fixture.Exec("GRANT USAGE ON SCHEMA public TO merge_denied_actor", 0))
					}
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION merge_denied_actor", 0))
					defer func() {
						if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
							t.Error(err)
						}
					}()
					for _, q := range []string{
						"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(1)",
						"MERGE INTO t USING (VALUES(99)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)",
					} {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
						s.Prepared, s.ErrorStage = prepared, "execute"
						fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, s)
					}
					fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
				})
			}
		})
	}
}
