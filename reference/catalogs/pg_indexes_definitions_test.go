package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGIndexesDefinitions(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgi_left", "pgi_right", "pgi_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(q string) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0)) }
			run("CREATE TABLE pgi_defs(unused INT,n INT,s TEXT,p TEXT)")
			cases := []struct{ name, tail, want string }{
				{"pgi_dirs", "(n ASC NULLS LAST,n ASC NULLS FIRST,n DESC NULLS LAST,n DESC NULLS FIRST)", "(n, n NULLS FIRST, n DESC NULLS LAST, n DESC)"},
				{"pgi_expr", "(lower(s),(n+1)) INCLUDE(p)", "(lower(s), ((n + 1))) INCLUDE (p)"},
				{"pgi_coll", `(s COLLATE "C",s COLLATE "POSIX")`, `(s COLLATE "C", s COLLATE "POSIX")`},
				{"pgi_class", "(n oid_ops DESC NULLS LAST) INCLUDE(p)", "(n oid_ops DESC NULLS LAST) INCLUDE (p)"},
			}
			for _, c := range cases {
				run("CREATE INDEX " + c.name + " ON pgi_defs" + c.tail)
			}
			run("ALTER TABLE pgi_defs DROP COLUMN unused")
			for _, c := range cases {
				row := fixture.PgIndexesRow("public", "pgi_defs", c.name, "CREATE INDEX "+c.name+" ON public.pgi_defs USING btree "+c.want)
				fixture.PgIndexesCheck(t, p, b, b.DB, c.name, row)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.PgIndexesSelect(b, fixture.PgIndexesProbe, [][]any{row}, c.name)))
			}
			run("CREATE UNIQUE INDEX pgi_unique ON pgi_defs(n) NULLS NOT DISTINCT")
			fixture.PgIndexesCheck(t, p, b, b.DB, "pgi_unique", fixture.PgIndexesRow("public", "pgi_defs", "pgi_unique", "CREATE UNIQUE INDEX pgi_unique ON public.pgi_defs USING btree (n) NULLS NOT DISTINCT"))
			for _, c := range []struct{ name, clause, suffix string }{{"pgi_constraint", "UNIQUE", ""}, {"pgi_constraint_nnd", "UNIQUE NULLS NOT DISTINCT", " NULLS NOT DISTINCT"}} {
				run("CREATE TABLE " + c.name + "_table(n INT CONSTRAINT " + c.name + " " + c.clause + ")")
				fixture.PgIndexesCheck(t, p, b, b.DB, c.name, fixture.PgIndexesRow("public", c.name+"_table", c.name, "CREATE UNIQUE INDEX "+c.name+" ON public."+c.name+"_table USING btree (n)"+c.suffix))
			}
			for _, method := range []string{"btree", "hash"} {
				name := "pgi_concurrent_" + method
				run("CREATE INDEX CONCURRENTLY " + name + " ON pgi_defs USING " + method + "(n)")
				fixture.PgIndexesCheck(t, p, b, b.DB, name, fixture.PgIndexesRow("public", "pgi_defs", name, "CREATE INDEX "+name+" ON public.pgi_defs USING "+method+" (n)"))
			}
			for _, schema := range []string{"pgi_left", "pgi_right", "pgi_moved"} {
				run("CREATE SCHEMA " + schema)
			}
			for _, schema := range []string{"pgi_left", "pgi_right"} {
				run("CREATE TABLE " + schema + ".pgi_same(n INT)")
				run("CREATE INDEX pgi_same_index ON " + schema + ".pgi_same(n)")
			}
			run("SET search_path TO public")
			expected := [][]any{fixture.PgIndexesRow("pgi_left", "pgi_same", "pgi_same_index", "CREATE INDEX pgi_same_index ON pgi_left.pgi_same USING btree (n)"), fixture.PgIndexesRow("pgi_right", "pgi_same", "pgi_same_index", "CREATE INDEX pgi_same_index ON pgi_right.pgi_same USING btree (n)")}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.PgIndexesSelect(b, "SELECT * FROM pg_catalog.pg_indexes WHERE indexname='pgi_same_index' ORDER BY schemaname", expected))
			run("ALTER TABLE pgi_left.pgi_same SET SCHEMA pgi_moved")
			expected[0] = fixture.PgIndexesRow("pgi_moved", "pgi_same", "pgi_same_index", "CREATE INDEX pgi_same_index ON pgi_moved.pgi_same USING btree (n)")
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.PgIndexesSelect(b, "SELECT * FROM pg_catalog.pg_indexes WHERE indexname='pgi_same_index' ORDER BY schemaname", expected))
			run(`CREATE TABLE public."pgi Table"("select" INT)`)
			run(`CREATE INDEX "pgi ""Index" ON public."pgi Table"("select")`)
			fixture.PgIndexesCheck(t, p, b, b.DB, `pgi "Index`, fixture.PgIndexesRow("public", "pgi Table", `pgi "Index`, `CREATE INDEX "pgi ""Index" ON public."pgi Table" USING btree ("select")`))
		})
	}
}
func TestReferencePGIndexesPhysicalBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE pgi_physical(n INT)", 0))
			for _, c := range []struct{ name, tail, message, position, suffix string }{
				{"pgi_storage", "WITH(fillfactor=80)", "index storage parameters are unsupported", "45", " WITH (fillfactor='80')"},
				{"pgi_space", "TABLESPACE pg_default", "index tablespaces are unsupported", "43", ""},
			} {
				q := "CREATE INDEX " + c.name + " ON pgi_physical(n) " + c.tail
				if b.PG {
					run(fixture.Exec(q, 0))
					fixture.PgIndexesCheck(t, p, b, b.DB, c.name, fixture.PgIndexesRow("public", "pgi_physical", c.name, "CREATE INDEX "+c.name+" ON public.pgi_physical USING btree (n)"+c.suffix))
				} else {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: c.message, Position: c.position})
					s.ErrorStage = "execute"
					run(s)
					// WITH binds at execution; TABLESPACE is an existing
					// Prepare-time capability boundary (see btree exclusions).
					if c.name == "pgi_space" {
						s.ErrorStage = "prepare"
					}
					run(fixture.Prepared(s))
					fixture.PgIndexesCheck(t, p, b, b.DB, c.name, nil)
				}
			}
		})
	}
}
