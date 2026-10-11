package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCheckPrivilegeDetailPostgres(t *testing.T) {
	for _, mode := range []string{"none", "readable", "columns", "table"} {
		t.Run(mode, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"check_actor"})
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				run(fixture.Exec("CREATE ROLE check_actor", 0))
				run(fixture.Exec("CREATE TABLE t(n INT,secret TEXT,readable TEXT,CONSTRAINT c CHECK(n=1 AND secret='private'))", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,'private','visible')", 1))
				run(fixture.Exec("GRANT UPDATE(n) ON t TO check_actor", 0))
				if backend.PG {
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO check_actor", 0))
				}
				detail := "n) = (2"
				switch mode {
				case "readable":
					run(fixture.Exec("GRANT SELECT(readable) ON t TO check_actor", 0))
					detail = "n, readable) = (2, visible"
				case "columns":
					run(fixture.Exec("GRANT SELECT(n,secret,readable) ON t TO check_actor", 0))
					detail = "n, secret, readable) = (2, private, visible"
				case "table":
					run(fixture.Exec("GRANT SELECT ON t TO check_actor", 0))
					detail = "2, private, visible"
				}
				run(fixture.Exec("SET ROLE check_actor", 0))
				run(fixture.ImplicitError("UPDATE t SET n=2", fixture.CheckViolation("t", "c", detail)))
				run(fixture.Prepared(fixture.ImplicitError("UPDATE t SET n=2", fixture.CheckViolation("t", "c", detail))))
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Select("SELECT * FROM t", []string{"n", "secret", "readable"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "private", "visible"}}))
			}
		})
	}
}
func TestReferenceCheckTableoidBoundary(t *testing.T) {
	if !*fixture.Mode {
		return
	} // Independently retained tableoid capability oracle.
	p := fixture.NewPair(t)
	t.Run("postgres_supported", func(t *testing.T) {
		for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(a INT CHECK(tableoid>0))", 0), fixture.Exec("INSERT INTO t VALUES(1)", 1), fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})} {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
		}
	})
	t.Run("native_existing_system_column_boundary", func(t *testing.T) {
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("CREATE TABLE t(a INT)", 0))
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.ImplicitError("SELECT tableoid FROM t", fixture.Diagnostic{Code: "42703", Message: `column "tableoid" does not exist`, Position: "8"}))
	})
}
