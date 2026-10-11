package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCheckInsertPrivilegeDetailPostgres(t *testing.T) {
	for _, mode := range []string{"none", "readable", "columns", "table"} {
		for _, prepared := range []bool{false, true} {
			t.Run(mode+strconv.FormatBool(prepared), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"check_actor"})
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step) }
					run(fixture.Exec("CREATE ROLE check_actor", 0))
					run(fixture.Exec("CREATE TABLE t(n INT,secret TEXT DEFAULT 'private',readable TEXT DEFAULT 'visible',CONSTRAINT c CHECK(n=1 AND secret='private'))", 0))
					if backend.PG {
						run(fixture.Exec("GRANT USAGE ON SCHEMA public TO check_actor", 0))
					}
					run(fixture.Exec("GRANT INSERT(n) ON t TO check_actor", 0))
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
					step := fixture.ImplicitError("INSERT INTO t(n) VALUES($1)", fixture.CheckViolation("t", "c", detail), 2)
					step.Prepared = prepared
					run(step)
					run(fixture.Exec("RESET ROLE", 0))
					run(fixture.Select("SELECT * FROM t", []string{"n", "secret", "readable"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{}))
				}
			})
		}
	}
}
