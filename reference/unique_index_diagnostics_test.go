package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestReferenceUniqueIndexDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		p := newReferenceScenario(t)
		for _, b := range p.backends() {
			t.Run(fmt.Sprintf("%s/prepared=%t", b.name, prepared), func(t *testing.T) {
				run := func(s referenceStep) {
					if prepared {
						s = referencePrepared(s)
					}
					referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				}
				run(referenceExec("CREATE TABLE ui_t(a INT,b TEXT)", 0))
				run(referenceExec("INSERT INTO ui_t VALUES(1,'x')", 1))
				run(referenceExec("CREATE UNIQUE INDEX ui_ab ON ui_t(a,b)", 0))
				for _, c := range []struct{ sql, code, message string }{
					{"CREATE UNIQUE INDEX bad ON missing(a)", "42P01", `relation "missing" does not exist`},
					{"CREATE UNIQUE INDEX bad ON ui_t(missing)", "42703", `column "missing" does not exist`},
					{"CREATE UNIQUE INDEX bad ON ui_t USING missing(a)", "42704", `access method "missing" does not exist`},
					{"CREATE UNIQUE INDEX ui_ab ON ui_t(a)", "42P07", `relation "ui_ab" already exists`},
					{"CREATE UNIQUE INDEX IF NOT EXISTS ui_ab ON ui_t(missing)", "42703", `column "missing" does not exist`},
					{"CREATE UNIQUE INDEX bad ON ui_t(" + strings.Repeat("a,", 32) + "a)", "54011", "cannot use more than 32 columns in an index"},
				} {
					s := referenceImplicitError(c.sql, referenceDiagnostic{Code: c.code, Message: c.message})
					s.errorStage = "execute"
					run(s)
				}
				run(referenceSelect("SELECT relname,relkind FROM pg_class WHERE relname='ui_ab' OR relname='bad' ORDER BY relname", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{"ui_ab", "i"}}))
				run(referenceSelect("SELECT a,b FROM ui_t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}}))
				// IF NOT EXISTS checks existence, not whether the original is unique.
				run(referenceExec("CREATE INDEX ui_plain ON ui_t(b)", 0))
				run(referenceExec("CREATE UNIQUE INDEX IF NOT EXISTS ui_plain ON ui_t(b)", 0))
				run(referenceExec("DROP INDEX ui_ab", 0))
				run(referenceExec("INSERT INTO ui_t VALUES(1,'x')", 1))
				run(referenceExec("DELETE FROM ui_t", 2))
				run(referenceExec("INSERT INTO ui_t VALUES(1,'x')", 1))
				run(referenceExec("CREATE UNIQUE INDEX ON ui_t(a,b)", 0))
				run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ui_t_a_b_idx'", []string{"relname"}, []string{"NAME"}, [][]any{{"ui_t_a_b_idx"}}))
			})
		}
	}
}

func TestReferenceUniqueIndexBuildOwnerDisclosure(t *testing.T) {
	for _, mode := range []string{"none", "partial", "all", "table"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", mode, prepared), func(t *testing.T) {
				roleReferenceCleanup(t, []string{"ui_owner"})
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						a, _ := referenceLockConnections(t, p.ctx, b.db)
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
						run(referenceExec("CREATE ROLE ui_owner", 0))
						if b.pg { run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO ui_owner", 0)) }
						run(referenceExec("SET SESSION AUTHORIZATION ui_owner", 0))
						run(referenceExec("CREATE TABLE ui_t(a INT,b TEXT)", 0))
						run(referenceExec("INSERT INTO ui_t VALUES(1,'protected'),(1,'protected')", 2))
						run(referenceExec("REVOKE SELECT ON ui_t FROM ui_owner", 0))
						if mode == "partial" || mode == "all" {
							run(referenceExec("GRANT SELECT(a) ON ui_t TO ui_owner", 0))
						}
						if mode == "all" {
							run(referenceExec("GRANT SELECT(b) ON ui_t TO ui_owner", 0))
						}
						if mode == "table" {
							run(referenceExec("GRANT SELECT ON ui_t TO ui_owner", 0))
						}
						detail := "Duplicate keys exist."
						if mode == "all" || mode == "table" {
							detail = "Key (a, b)=(1, protected) is duplicated."
						}
						s := referenceErrorDiagnostic("CREATE UNIQUE INDEX ui_ab ON ui_t(a,b)", uniqueIndexReferenceBuildDiagnostic("ui_t", "ui_ab", detail))
						s.errorStage = "execute"
						if prepared {
							s = referencePrepared(s)
						}
						run(s)
						run(referenceExec("RESET SESSION AUTHORIZATION", 0))
						run(referenceSelect("SELECT a,b FROM ui_t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "protected"}, {int64(1), "protected"}}))
						run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ui_ab'", []string{"relname"}, []string{"NAME"}, nil))
					})
				}
			})
		}
	}
}
