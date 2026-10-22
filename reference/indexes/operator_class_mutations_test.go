package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func classDefaultDefinition(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
	run(fixture.Exec(`CREATE TYPE public.class_default_label AS ENUM('low','high')`, 0))
	run(fixture.Exec(`CREATE OPERATOR CLASS public.class_default_label DEFAULT FOR TYPE public.class_default_label USING btree AS FUNCTION 1 (public.class_default_label,public.class_default_label) pg_catalog.enum_cmp(anyenum,anyenum)`, 0))
	run(fixture.Select(`SELECT c.opcdefault,c.opcintype=t.oid,c.opcmethod::bigint,c.opckeytype::bigint FROM pg_opclass c JOIN pg_type t ON t.oid=c.opcintype WHERE c.opcname='class_default_label' AND t.typname='class_default_label'`, []string{"opcdefault", "?column?", "opcmethod", "opckeytype"}, []string{"BOOL", "BOOL", "INT8", "INT8"}, [][]any{{true, true, int64(403), int64(0)}}))
	// Incomplete class admission does not promise executable operator completeness.
	// Remove it before indexing so the ordinary polymorphic enum class is selected.
	run(fixture.Exec(`DROP OPERATOR CLASS public.class_default_label USING btree`, 0))
	run(fixture.Exec(`CREATE TABLE class_default_rows(e class_default_label)`, 0))
	run(fixture.Exec(`INSERT INTO class_default_rows VALUES('high'),('low')`, 2))
	run(fixture.Exec(`CREATE INDEX class_default_index ON class_default_rows(e)`, 0))
	run(fixture.Exec(`INSERT INTO class_default_rows VALUES('high')`, 1))
	classRoutes(t, p, b, conn, fixture.Select(`SELECT e::text FROM class_default_rows ORDER BY class_default_rows.e`, []string{"e"}, []string{"TEXT"}, [][]any{{"low"}, {"high"}, {"high"}}))
}

func classMutationBoundaries(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
	for _, q := range []string{
		`CREATE OPERATOR CLASS public.class_mutation_a FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`,
		`CREATE OPERATOR CLASS public.class_mutation_b FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`,
		`CREATE SCHEMA class_mutation_schema`,
		`CREATE OPERATOR CLASS class_mutation_schema.class_mutation_a FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`,
	} {
		run(fixture.Exec(q, 0))
	}
	const q = `SELECT c.opcname,n.nspname,c.opcmethod::bigint,c.opcfamily=f.oid,f.opfname FROM pg_opclass c JOIN pg_namespace n ON n.oid=c.opcnamespace JOIN pg_opfamily f ON f.oid=c.opcfamily WHERE c.opcname IN('class_mutation_a','class_mutation_b') ORDER BY c.opcname,n.nspname`
	check := func() {
		run(fixture.Select(q, []string{"opcname", "nspname", "opcmethod", "?column?", "opfname"}, []string{"NAME", "NAME", "INT8", "BOOL", "NAME"}, [][]any{{"class_mutation_a", "class_mutation_schema", int64(403), true, "class_mutation_a"}, {"class_mutation_a", "public", int64(403), true, "class_mutation_a"}, {"class_mutation_b", "public", int64(403), true, "class_mutation_b"}}))
	}
	for _, tc := range []struct{ q, message string }{
		{`ALTER OPERATOR CLASS public.class_mutation_a USING btree RENAME TO class_mutation_b`, `operator class "class_mutation_b" for access method "btree" already exists in schema "public"`},
		{`ALTER OPERATOR CLASS public.class_mutation_a USING btree SET SCHEMA class_mutation_schema`, `operator class "class_mutation_a" for access method "btree" already exists in schema "class_mutation_schema"`},
	} {
		t.Run("collision_"+tc.q, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42710", Message: tc.message})
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				check()
			}
		})
	}
	run(fixture.Exec(`GRANT USAGE ON SCHEMA public TO class_life_other`, 0))
	run(fixture.Exec(`SET ROLE class_life_other`, 0))
	for _, tc := range []struct{ q, message string }{
		{`ALTER OPERATOR CLASS public.class_mutation_a USING btree RENAME TO denied`, `must be owner of operator class class_mutation_a`},
		{`ALTER OPERATOR CLASS public.class_mutation_a USING btree SET SCHEMA public`, `permission denied for schema public`},
		{`ALTER OPERATOR CLASS public.class_mutation_a USING btree OWNER TO class_life_owner`, `must be owner of operator class class_mutation_a`},
		{`DROP OPERATOR CLASS public.class_mutation_a USING btree`, `must be owner of operator class btree.public.class_mutation_a`},
	} {
		t.Run("denied_"+tc.q, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42501", Message: tc.message})
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			}
		})
	}
	run(fixture.Exec(`RESET ROLE`, 0))
	check()
	run(fixture.Exec(`DROP SCHEMA class_mutation_schema CASCADE`, 0))
}
