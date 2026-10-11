package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterTypeViewDependencies(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"where", "SELECT b FROM t WHERE a=1"},
		{"order", "SELECT b FROM t ORDER BY a"},
		{"join", "SELECT t.b FROM t JOIN u ON t.a=u.c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,7)", 1))
					run(fixture.Exec("CREATE TABLE u(c INT)", 0))
					run(fixture.Exec("INSERT INTO u VALUES(1)", 1))
					run(fixture.Exec("CREATE VIEW v AS "+tc.query, 0))
					run(fixture.ImplicitError("ALTER TABLE t ALTER a TYPE BIGINT", fixture.Diagnostic{Code: "0A000", Message: "cannot alter type of a column used by a view or rule", Detail: `rule _RETURN on view v depends on column "a"`}))
					run(fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}}))
					run(fixture.Select("SELECT * FROM v", []string{"b"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			}
		})
	}
}

func TestReferenceAlterTypeViewIdentityCollationAndCascade(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "s")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			t.Cleanup(func() {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP SCHEMA IF EXISTS s CASCADE", 0))
			})
			run(fixture.Exec("DROP SCHEMA IF EXISTS s CASCADE", 0))
			run(fixture.Exec("CREATE SCHEMA s", 0))
			run(fixture.Exec("CREATE TABLE t(c VARCHAR(5),discard INT,keep INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES('seven',1,9)", 1))
			run(fixture.Exec(`CREATE VIEW s."Odd View" AS SELECT c,discard FROM public.t`, 0))
			run(fixture.Exec("CREATE VIEW v_keep AS SELECT keep FROM t", 0))
			for _, action := range []string{"TYPE VARCHAR(5)", `TYPE VARCHAR(5) COLLATE "C"`, `TYPE VARCHAR(5) COLLATE "default"`} {
				failure := fixture.ImplicitError("ALTER TABLE public.t ALTER c "+action, fixture.Diagnostic{Code: "0A000", Message: "cannot alter type of a column used by a view or rule", Detail: `rule _RETURN on view s."Odd View" depends on column "c"`})
				failure.ErrorStage = "execute"
				run(failure)
				if b.Name == "postgres" {
					run(fixture.Select("SELECT atttypmod,attcollation::int FROM pg_attribute WHERE attrelid='t'::regclass AND attname='c'", []string{"atttypmod", "attcollation"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(100)}}))
				}
				run(fixture.Select(`SELECT * FROM s."Odd View"`, []string{"c", "discard"}, []string{"VARCHAR", "INT4"}, [][]any{{"seven", int64(1)}}))
			}
			// DROP runs first and removes the view which also blocked c.
			run(fixture.Exec("ALTER TABLE t ALTER c TYPE TEXT, DROP discard CASCADE", 0))
			run(fixture.Select("SELECT * FROM t", []string{"c", "keep"}, []string{"TEXT", "INT4"}, [][]any{{"seven", int64(9)}}))
			run(fixture.Select("SELECT keep FROM v_keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(9)}}))
			absent := fixture.ImplicitError(`SELECT * FROM s."Odd View"`, fixture.Diagnostic{Code: "42P01", Message: `relation "s.Odd View" does not exist`, Position: "15"})
			absent.SelectRows, absent.ErrorStage = true, "execute"
			run(absent)
		})
	}
}
