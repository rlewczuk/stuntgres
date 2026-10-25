package reference_test

import (
	"strings"
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
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			if b.PG {
				// Keep the independent CHECK capability witness, without
				// expanding native CHECK's consumer namespace.
				run(fixture.Exec("CREATE TABLE t(a INT CHECK(tableoid>0))", 0))
			} else {
				rejected := "CREATE TABLE toi_check_rejected(a INT,CONSTRAINT toi_check_rejected_ck CHECK(tableoid>0))"
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(rejected, fixture.Diagnostic{Code: "42703", Message: `column "tableoid" does not exist`, Position: fixture.Position(strings.Index(rejected, "tableoid") + 1)})
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					run(fixture.Select("SELECT count(*) AS n FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname='toi_check_rejected'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Select("SELECT count(*) AS n FROM pg_constraint WHERE conname='toi_check_rejected_ck'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				run(fixture.Exec("CREATE TABLE t(a INT)", 0))
			}
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT a,tableoid='t'::regclass::oid AS physical FROM t", []string{"a", "physical"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
				s.Prepared = prepared
				run(s)
			}
		})
	}
}
