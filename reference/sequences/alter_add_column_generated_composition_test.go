package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnGeneratedComposition(t *testing.T) {
	t.Run("completed-row", func(t *testing.T) {
		addGeneratedRoutes(t, func(g *addGeneratedCase) {
			g.exec("CREATE TABLE g(a integer,old integer GENERATED ALWAYS AS(a+1) STORED)", 0)
			g.exec("INSERT INTO g(a) VALUES(2)", 1)
			g.exec("ALTER TABLE g ADD operand integer DEFAULT 5, ADD s integer GENERATED ALWAYS AS(a+operand) STORED, ALTER old SET EXPRESSION AS(a+3)", 0)
			g.run(fixture.Select("SELECT a,old,operand,s FROM g", []string{"a", "old", "operand", "s"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(5), int64(5), int64(7)}}))
			g.exec("ALTER TABLE g ADD constant integer GENERATED ALWAYS AS(7) STORED, ADD nullable integer GENERATED ALWAYS AS(NULL) STORED, ADD checked integer GENERATED ALWAYS AS(a+1) STORED NOT NULL CONSTRAINT positive CHECK(checked>0)", 0)
			g.run(fixture.Select("SELECT constant,nullable,checked FROM g", []string{"constant", "nullable", "checked"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), nil, int64(3)}}))
			g.exec("ALTER TABLE g ADD v integer GENERATED ALWAYS AS(a/0) VIRTUAL", 0)
			g.run(fixture.Select("SELECT a,old,operand,s FROM g", []string{"a", "old", "operand", "s"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(5), int64(5), int64(7)}}))
			g.fail("SELECT v FROM g", addGeneratedDiagnostic("22012", "division by zero"), true, "execute")
			g.exec("CREATE TABLE typed_values(a integer)", 0)
			g.exec("INSERT INTO typed_values VALUES(2)", 1)
			g.exec("ALTER TABLE typed_values ALTER a TYPE bigint USING a::bigint+10, ADD b bigint GENERATED ALWAYS AS(a+1) STORED", 0)
			g.exec("ALTER TABLE typed_values ADD v bigint GENERATED ALWAYS AS(a/0) VIRTUAL", 0)
			g.run(fixture.Select("SELECT a,b FROM typed_values", []string{"a", "b"}, []string{"INT8", "INT8"}, [][]any{{int64(12), int64(13)}}))
			g.fail("SELECT v FROM typed_values", addGeneratedDiagnostic("22012", "division by zero"), true, "execute")
		})
	})
	t.Run("combined-virtual-rewrite", func(t *testing.T) {
		addGeneratedRoutes(t, func(g *addGeneratedCase) {
			g.exec("CREATE TABLE g(a integer,old integer GENERATED ALWAYS AS(a+1) STORED)", 0)
			g.exec("INSERT INTO g(a) VALUES(2)", 1)
			g.fail("ALTER TABLE g ADD operand integer DEFAULT 5, ADD s integer GENERATED ALWAYS AS(a+operand) STORED, ADD v integer GENERATED ALWAYS AS(a/0) VIRTUAL, ALTER old SET EXPRESSION AS(a+3)", addGeneratedDiagnostic("22012", "division by zero"), false, "execute")
			g.run(fixture.Select("SELECT * FROM g", []string{"a", "old"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
			g.kinds("g", []any{"old", "s"})
			g.run(fixture.Select("SELECT adnum,pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid='g'::regclass", []string{"adnum", "pg_get_expr"}, []string{"INT2", "TEXT"}, [][]any{{int64(2), "(a + 1)"}}))
			g.run(fixture.Select("SELECT column_name,dependent_column FROM information_schema.column_column_usage WHERE table_name='g'", []string{"column_name", "dependent_column"}, []string{"NAME", "NAME"}, [][]any{{"a", "old"}}))
			g.run(fixture.Select("SELECT d.deptype,d.refobjsubid FROM pg_depend d WHERE d.classid='pg_attrdef'::regclass AND d.refclassid='pg_class'::regclass AND d.refobjid='g'::regclass ORDER BY d.deptype,d.refobjsubid", []string{"deptype", "refobjsubid"}, []string{"CHAR", "INT4"}, [][]any{{"i", int64(2)}, {"n", int64(1)}}))
			g.exec("ALTER TABLE g ADD operand integer DEFAULT 5, ADD s integer GENERATED ALWAYS AS(a+operand) STORED, ALTER old SET EXPRESSION AS(a+3)", 0)
			g.run(fixture.Select("SELECT * FROM g", []string{"a", "old", "operand", "s"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(5), int64(5), int64(7)}}))
		})
	})
	t.Run("combined-control-virtual-rewrite", func(t *testing.T) {
		addGeneratedRoutes(t, func(g *addGeneratedCase) {
			g.exec("CREATE TABLE g(a integer)", 0)
			g.exec("INSERT INTO g VALUES(2)", 1)
			g.fail("ALTER TABLE g ADD s integer GENERATED ALWAYS AS(a+1) STORED, ADD v integer GENERATED ALWAYS AS(a*2) VIRTUAL, ADD lazy integer GENERATED ALWAYS AS(a/0)", addGeneratedDiagnostic("22012", "division by zero"), false, "execute")
			g.unchanged([][]any{{int64(2)}})
			g.run(fixture.Select("SELECT deptype,refobjsubid FROM pg_depend WHERE classid='pg_attrdef'::regclass AND refclassid='pg_class'::regclass AND refobjid='g'::regclass", []string{"deptype", "refobjsubid"}, []string{"CHAR", "INT4"}, nil))
			g.exec("ALTER TABLE g ADD s integer GENERATED ALWAYS AS(a+1) STORED, ADD v integer GENERATED ALWAYS AS(a*2) VIRTUAL", 0)
			g.run(fixture.Select("SELECT * FROM g", []string{"a", "s", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(3), int64(4)}}))
		})
	})
	t.Run("add-set", func(t *testing.T) {
		addGeneratedRoutes(t, func(g *addGeneratedCase) {
			g.exec("CREATE TABLE g(a integer)", 0)
			g.exec("INSERT INTO g VALUES(2)", 1)
			g.exec("ALTER TABLE g ADD b integer GENERATED ALWAYS AS(a+1) STORED, ALTER b SET EXPRESSION AS(a+2)", 0)
			g.run(fixture.Select("SELECT a,b FROM g", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}}))
			g.kinds("g", []any{"b", "s"})
			g.run(fixture.Select("SELECT pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid='g'::regclass", []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{"(a + 2)"}}))
			g.run(fixture.Select("SELECT column_name,dependent_column FROM information_schema.column_column_usage WHERE table_name='g'", []string{"column_name", "dependent_column"}, []string{"NAME", "NAME"}, [][]any{{"a", "b"}}))
			g.exec("INSERT INTO g(a) VALUES(3)", 1)
			g.run(fixture.Select("SELECT a,b FROM g ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}, {int64(3), int64(5)}}))
			g.exec("UPDATE g SET a=4 WHERE a=2", 1)
			g.run(fixture.Select("SELECT a,b FROM g ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(5)}, {int64(4), int64(6)}}))
		})
	})
	for _, tc := range []struct{ name, actions, code, message, detail string }{
		{"forward", "ADD b integer GENERATED ALWAYS AS(later+1) STORED, ADD later integer DEFAULT 5", "42703", `column "later" does not exist`, ""},
		{"self", "ADD b integer GENERATED ALWAYS AS(b+1) STORED", "42P17", `cannot use generated column "b" in column generation expression`, "A generated column cannot reference another generated column."},
		{"previous-generated", "ADD b integer GENERATED ALWAYS AS(a+1) STORED, ADD c integer GENERATED ALWAYS AS(b+1) VIRTUAL", "42P17", `cannot use generated column "b" in column generation expression`, "A generated column cannot reference another generated column."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addGeneratedRoutes(t, func(g *addGeneratedCase) {
				g.exec("CREATE TABLE g(a integer)", 0)
				g.exec("INSERT INTO g VALUES(2)", 1)
				d := addGeneratedDiagnostic(tc.code, tc.message)
				d.Detail = tc.detail
				g.fail("ALTER TABLE g "+tc.actions, d, false, "execute")
				g.unchanged([][]any{{int64(2)}})
			})
		})
	}
	t.Run("inheritance", func(t *testing.T) {
		addGeneratedRoutes(t, func(g *addGeneratedCase) {
			g.exec("CREATE TABLE parent(a integer)", 0)
			g.exec("CREATE TABLE child() INHERITS(parent)", 0)
			g.exec("INSERT INTO parent VALUES(1)", 1)
			g.exec("INSERT INTO child VALUES(2)", 1)
			g.exec("ALTER TABLE parent ADD s integer GENERATED ALWAYS AS(a+1) STORED", 0)
			g.exec("ALTER TABLE parent ADD v integer GENERATED ALWAYS AS(a*2) VIRTUAL", 0)
			g.run(fixture.Select("SELECT * FROM parent ORDER BY a", []string{"a", "s", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(2)}, {int64(2), int64(3), int64(4)}}))
			g.exec("INSERT INTO child(a) VALUES(3)", 1)
			g.run(fixture.Select("SELECT * FROM child ORDER BY a", []string{"a", "s", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(3), int64(4)}, {int64(3), int64(4), int64(6)}}))
			g.kinds("child", []any{"s", "s"}, []any{"v", "v"})
			d := addGeneratedDiagnostic("42611", `column "s" inherits from generated column of different kind`)
			d.Detail = "Parent column is STORED, child column is VIRTUAL."
			g.fail("CREATE TABLE wrong_kind(s integer GENERATED ALWAYS AS(a+3) VIRTUAL) INHERITS(parent)", d, false, "execute")
			g.run(fixture.Select("SELECT * FROM child ORDER BY a", []string{"a", "s", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(3), int64(4)}, {int64(3), int64(4), int64(6)}}))
		})
	})
	t.Run("typed-partition-authority", func(t *testing.T) {
		addGeneratedRoutes(t, func(g *addGeneratedCase) {
			g.exec("CREATE TYPE typed_row AS(a integer)", 0)
			g.exec("CREATE TABLE typed_g OF typed_row", 0)
			g.exec("INSERT INTO typed_g VALUES(2)", 1)
			g.fail("ALTER TABLE typed_g ADD b integer GENERATED ALWAYS AS(a+1) STORED", addGeneratedDiagnostic("42809", "cannot add column to typed table"), false, "execute")
			g.run(fixture.Select("SELECT * FROM typed_g", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}))
			g.exec("CREATE TABLE partitioned(a integer) PARTITION BY RANGE(a)", 0)
			g.exec("CREATE TABLE leaf PARTITION OF partitioned DEFAULT", 0)
			g.exec("INSERT INTO partitioned VALUES(2)", 1)
			q := "ALTER TABLE partitioned ADD b integer GENERATED ALWAYS AS(a+1) STORED"
			if g.b.PG {
				g.exec(q, 0)
				g.run(fixture.Select("SELECT * FROM partitioned", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
			} else {
				g.fail(q, addGeneratedDiagnostic("0A000", "altering partition hierarchies is unsupported"), false, "execute")
				g.run(fixture.Select("SELECT * FROM partitioned", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}))
				g.kinds("partitioned")
				g.kinds("leaf")
			}
			g.exec("BEGIN READ ONLY", 0)
			g.fail("ALTER TABLE missing ADD b integer GENERATED ALWAYS AS(1/0) STORED", addGeneratedDiagnostic("25006", "cannot execute ALTER TABLE in a read-only transaction"), false, "execute")
			g.exec("ROLLBACK", 0)
		})
	})
	t.Run("virtual-security", func(t *testing.T) {
		addGeneratedRoutes(t, func(g *addGeneratedCase) {
			g.exec("CREATE DOMAIN generated_domain AS integer", 0)
			g.exec("CREATE TABLE g(a integer)", 0)
			g.exec("INSERT INTO g VALUES(2)", 1)
			for _, def := range []string{"b integer GENERATED ALWAYS AS((a::generated_domain)::integer) VIRTUAL", "b integer GENERATED ALWAYS AS(CASE WHEN false THEN (a::generated_domain)::integer ELSE a END)", "b integer GENERATED ALWAYS AS((NULL::generated_domain)::integer) VIRTUAL"} {
				d := addGeneratedDiagnostic("0A000", "generation expression uses user-defined type")
				d.Detail = "Virtual generated columns that make use of user-defined types are not yet supported."
				g.fail("ALTER TABLE g ADD "+def, d, false, "execute")
				g.unchanged([][]any{{int64(2)}})
			}
			g.fail("ALTER TABLE g ADD b generated_domain GENERATED ALWAYS AS(a+1) VIRTUAL", addGeneratedDiagnostic("0A000", `virtual generated column "b" cannot have a domain type`), false, "execute")
			g.exec("CREATE FUNCTION public.add_generated_udf(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS 'SELECT $1+1'", 0)
			d := addGeneratedDiagnostic("0A000", "generation expression uses user-defined function")
			d.Detail = "Virtual generated columns that make use of user-defined functions are not yet supported."
			g.fail("ALTER TABLE g ADD b integer GENERATED ALWAYS AS(CASE WHEN false THEN public.add_generated_udf(a) ELSE a END) VIRTUAL", d, false, "execute")
			g.unchanged([][]any{{int64(2)}})
		})
	})
	t.Run("rewrite-events", addGeneratedRewriteEvents)
	t.Run("stored-domain-default", func(t *testing.T) {
		addGeneratedRoutes(t, func(g *addGeneratedCase) {
			g.exec("CREATE DOMAIN generated_destination AS integer DEFAULT 99 CHECK(VALUE>0)", 0)
			g.exec("CREATE TABLE g(a integer)", 0)
			g.exec("INSERT INTO g VALUES(2)", 1)
			g.exec("ALTER TABLE g ADD b generated_destination GENERATED ALWAYS AS(a+1) STORED", 0)
			g.run(fixture.Select("SELECT a,b::integer FROM g", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
			g.exec("INSERT INTO g(a) VALUES(4)", 1)
			g.run(fixture.Select("SELECT a,b::integer FROM g ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}, {int64(4), int64(5)}}))
			g.run(fixture.Select("SELECT column_default FROM information_schema.columns WHERE table_name='g' AND column_name='b'", []string{"column_default"}, []string{"VARCHAR"}, [][]any{{nil}}))
		})
	})
	t.Run("owner", addGeneratedOwner)
	t.Run("lock-cancellation", addGeneratedLockCancellation)
}

func addGeneratedOwner(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"add_generated_actor"})
	addGeneratedRoutes(t, func(g *addGeneratedCase) {
		g.exec("CREATE ROLE add_generated_actor", 0)
		defer func() {
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP OWNED BY add_generated_actor CASCADE", "DROP ROLE add_generated_actor"} {
				if _, err := g.c.ExecContext(g.p.Ctx, q); err != nil {
					g.t.Error("role cleanup", err)
				}
			}
		}()
		g.exec("CREATE TABLE g(a integer)", 0)
		g.exec("INSERT INTO g VALUES(2)", 1)
		g.exec("GRANT USAGE ON SCHEMA public TO add_generated_actor", 0)
		g.exec("GRANT ALL ON g TO add_generated_actor", 0)
		g.exec("SET SESSION AUTHORIZATION add_generated_actor", 0)
		g.fail("ALTER TABLE g ADD b integer GENERATED ALWAYS AS(a/0) STORED", addGeneratedDiagnostic("42501", "must be owner of table g"), false, "execute")
		g.exec("RESET SESSION AUTHORIZATION", 0)
		g.unchanged([][]any{{int64(2)}})
	})
}
