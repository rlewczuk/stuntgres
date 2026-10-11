package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUniqueIndexDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(fmt.Sprintf("%s/prepared=%t", b.Name, prepared), func(t *testing.T) {
				run := func(s fixture.Step) {
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
				run(fixture.Exec("CREATE TABLE ui_t(a INT,b TEXT)", 0))
				run(fixture.Exec("INSERT INTO ui_t VALUES(1,'x')", 1))
				run(fixture.Exec("CREATE UNIQUE INDEX ui_ab ON ui_t(a,b)", 0))
				for _, c := range []struct{ sql, code, message string }{
					{"CREATE UNIQUE INDEX bad ON missing(a)", "42P01", `relation "missing" does not exist`},
					{"CREATE UNIQUE INDEX bad ON ui_t(missing)", "42703", `column "missing" does not exist`},
					{"CREATE UNIQUE INDEX bad ON ui_t USING missing(a)", "42704", `access method "missing" does not exist`},
					{"CREATE UNIQUE INDEX ui_ab ON ui_t(a)", "42P07", `relation "ui_ab" already exists`},
					{"CREATE UNIQUE INDEX IF NOT EXISTS ui_ab ON ui_t(missing)", "42703", `column "missing" does not exist`},
					{"CREATE UNIQUE INDEX bad ON ui_t(" + strings.Repeat("a,", 32) + "a)", "54011", "cannot use more than 32 columns in an index"},
				} {
					s := fixture.ImplicitError(c.sql, fixture.Diagnostic{Code: c.code, Message: c.message})
					s.ErrorStage = "execute"
					run(s)
				}
				run(fixture.Select("SELECT relname,relkind FROM pg_class WHERE relname='ui_ab' OR relname='bad' ORDER BY relname", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{"ui_ab", "i"}}))
				run(fixture.Select("SELECT a,b FROM ui_t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}}))
				// IF NOT EXISTS checks existence, not whether the original is unique.
				run(fixture.Exec("CREATE INDEX ui_plain ON ui_t(b)", 0))
				run(fixture.Exec("CREATE UNIQUE INDEX IF NOT EXISTS ui_plain ON ui_t(b)", 0))
				run(fixture.Exec("DROP INDEX ui_ab", 0))
				run(fixture.Exec("INSERT INTO ui_t VALUES(1,'x')", 1))
				run(fixture.Exec("DELETE FROM ui_t", 2))
				run(fixture.Exec("INSERT INTO ui_t VALUES(1,'x')", 1))
				run(fixture.Exec("CREATE UNIQUE INDEX ON ui_t(a,b)", 0))
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ui_t_a_b_idx'", []string{"relname"}, []string{"NAME"}, [][]any{{"ui_t_a_b_idx"}}))
			})
		}
	}
}

func TestReferenceUniqueIndexBuildOwnerDisclosure(t *testing.T) {
	for _, mode := range []string{"none", "partial", "all", "table"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", mode, prepared), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"ui_owner"})
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
						run(fixture.Exec("CREATE ROLE ui_owner", 0))
						if b.PG {
							run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO ui_owner", 0))
						}
						run(fixture.Exec("SET SESSION AUTHORIZATION ui_owner", 0))
						run(fixture.Exec("CREATE TABLE ui_t(a INT,b TEXT)", 0))
						run(fixture.Exec("INSERT INTO ui_t VALUES(1,'protected'),(1,'protected')", 2))
						run(fixture.Exec("REVOKE SELECT ON ui_t FROM ui_owner", 0))
						if mode == "partial" || mode == "all" {
							run(fixture.Exec("GRANT SELECT(a) ON ui_t TO ui_owner", 0))
						}
						if mode == "all" {
							run(fixture.Exec("GRANT SELECT(b) ON ui_t TO ui_owner", 0))
						}
						if mode == "table" {
							run(fixture.Exec("GRANT SELECT ON ui_t TO ui_owner", 0))
						}
						detail := "Duplicate keys exist."
						if mode == "all" || mode == "table" {
							detail = "Key (a, b)=(1, protected) is duplicated."
						}
						s := fixture.ErrorDiagnostic("CREATE UNIQUE INDEX ui_ab ON ui_t(a,b)", uniqueIndexReferenceBuildDiagnostic("ui_t", "ui_ab", detail))
						s.ErrorStage = "execute"
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
						run(fixture.Select("SELECT a,b FROM ui_t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "protected"}, {int64(1), "protected"}}))
						run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ui_ab'", []string{"relname"}, []string{"NAME"}, nil))
					})
				}
			})
		}
	}
}
