package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnGeneratedMetadata(t *testing.T) {
	addGeneratedRoutes(t, func(g *addGeneratedCase) {
		g.exec("CREATE TABLE g(junk integer,a integer)", 0)
		g.exec("INSERT INTO g VALUES(9,2)", 1)
		g.exec("ALTER TABLE g DROP junk", 0)
		g.exec("ALTER TABLE g ADD s integer GENERATED ALWAYS AS(a+a) STORED, ADD v integer GENERATED ALWAYS AS(a+1) VIRTUAL, ADD d integer GENERATED ALWAYS AS(a+2)", 0)
		g.exec("CREATE TABLE control(a integer,s integer GENERATED ALWAYS AS(a+a) STORED,v integer GENERATED ALWAYS AS(a+1) VIRTUAL,d integer GENERATED ALWAYS AS(a+2))", 0)
		for _, table := range []string{"g", "control"} {
			g.kinds(table, []any{"s", "s"}, []any{"v", "v"}, []any{"d", "v"})
			g.run(fixture.Select("SELECT column_name,is_generated,generation_expression,column_default FROM information_schema.columns WHERE table_name=$1 AND is_generated='ALWAYS' ORDER BY ordinal_position", []string{"column_name", "is_generated", "generation_expression", "column_default"}, []string{"NAME", "VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{"s", "ALWAYS", "(a + a)", nil}, {"v", "ALWAYS", "(a + 1)", nil}, {"d", "ALWAYS", "(a + 2)", nil}}, table))
			g.run(fixture.Select("SELECT a.attname,d.oid>0 AS valid_oid,d.adrelid=a.attrelid AS owner,d.adnum=a.attnum AS attribute,pg_get_expr(d.adbin,d.adrelid) FROM pg_attribute a JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid=$1::regclass ORDER BY a.attnum", []string{"attname", "valid_oid", "owner", "attribute", "pg_get_expr"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "TEXT"}, [][]any{{"s", true, true, true, "(a + a)"}, {"v", true, true, true, "(a + 1)"}, {"d", true, true, true, "(a + 2)"}}, table))
			g.run(fixture.Select("SELECT column_name,dependent_column FROM information_schema.column_column_usage WHERE table_name=$1 ORDER BY dependent_column", []string{"column_name", "dependent_column"}, []string{"NAME", "NAME"}, [][]any{{"a", "d"}, {"a", "s"}, {"a", "v"}}, table))
			g.run(fixture.Select("SELECT a.attname,p.deptype,r.attname AS referenced FROM pg_attrdef d JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum JOIN pg_depend p ON p.classid='pg_attrdef'::regclass AND p.objid=d.oid JOIN pg_attribute r ON r.attrelid=p.refobjid AND r.attnum=p.refobjsubid WHERE d.adrelid=$1::regclass AND p.refclassid='pg_class'::regclass ORDER BY a.attname,p.deptype,r.attname", []string{"attname", "deptype", "referenced"}, []string{"NAME", "CHAR", "NAME"}, [][]any{{"d", "i", "d"}, {"d", "n", "a"}, {"s", "i", "s"}, {"s", "n", "a"}, {"v", "i", "v"}, {"v", "n", "a"}}, table))
		}
		var definition int64
		if err := g.c.QueryRowContext(g.p.Ctx, "SELECT oid FROM pg_attrdef WHERE adrelid='g'::regclass AND adnum=3").Scan(&definition); err != nil || definition <= 0 {
			g.t.Fatal(definition, err)
		}
		g.exec("ALTER TABLE g RENAME a TO base", 0)
		g.exec("ALTER TABLE g RENAME s TO stored", 0)
		g.run(fixture.Select("SELECT oid=$1::oid AS stable,adnum,pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE oid=$1::oid", []string{"stable", "adnum", "pg_get_expr"}, []string{"BOOL", "INT2", "TEXT"}, [][]any{{true, int64(3), "(base + base)"}}, definition))
		g.run(fixture.Select("SELECT * FROM g", []string{"base", "stored", "v", "d"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(4), int64(3), int64(4)}}))
		g.exec("ALTER TABLE g DROP v", 0)
		g.run(fixture.Select("SELECT column_name,dependent_column FROM information_schema.column_column_usage WHERE table_name='g' ORDER BY dependent_column", []string{"column_name", "dependent_column"}, []string{"NAME", "NAME"}, [][]any{{"base", "d"}, {"base", "stored"}}))
		g.exec("ALTER TABLE g ADD v integer GENERATED ALWAYS AS(base+8) VIRTUAL", 0)
		g.run(fixture.Select("SELECT column_name,dependent_column FROM information_schema.column_column_usage WHERE table_name='g' ORDER BY dependent_column", []string{"column_name", "dependent_column"}, []string{"NAME", "NAME"}, [][]any{{"base", "d"}, {"base", "stored"}, {"base", "v"}}))
		g.exec("CREATE TABLE dep(a integer,z integer)", 0)
		g.exec("INSERT INTO dep VALUES(2,9)", 1)
		g.exec("ALTER TABLE dep ADD b integer GENERATED ALWAYS AS(a+1) STORED", 0)
		d := addGeneratedDiagnostic("2BP01", `cannot drop column a of table dep because other objects depend on it`)
		d.Detail = `column b of table dep depends on column a of table dep`
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		g.fail("ALTER TABLE dep DROP a", d, false, "execute")
		g.run(fixture.Select("SELECT * FROM dep", []string{"a", "z", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(9), int64(3)}}))
		g.exec("ALTER TABLE dep DROP a CASCADE", 0)
		g.run(fixture.Select("SELECT * FROM dep", []string{"z"}, []string{"INT4"}, [][]any{{int64(9)}}))
		g.run(fixture.Select("SELECT adnum FROM pg_attrdef WHERE adrelid='dep'::regclass", []string{"adnum"}, []string{"INT2"}, nil))
		g.run(fixture.Select("SELECT column_name,dependent_column FROM information_schema.column_column_usage WHERE table_name='dep'", []string{"column_name", "dependent_column"}, []string{"NAME", "NAME"}, nil))
		g.exec("CREATE FUNCTION generated_provider(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS 'SELECT $1+1'", 0)
		g.exec("CREATE TABLE providers(a integer)", 0)
		g.exec("INSERT INTO providers VALUES(2)", 1)
		g.exec("ALTER TABLE providers ADD b integer GENERATED ALWAYS AS(generated_provider(a)) STORED", 0)
		g.run(fixture.Select("SELECT p.proname,d.deptype FROM pg_attrdef a JOIN pg_depend d ON d.classid='pg_attrdef'::regclass AND d.objid=a.oid JOIN pg_proc p ON d.refclassid='pg_proc'::regclass AND p.oid=d.refobjid WHERE a.adrelid='providers'::regclass", []string{"proname", "deptype"}, []string{"NAME", "CHAR"}, [][]any{{"generated_provider", "n"}}))
		g.run(fixture.Select("SELECT a,b FROM providers", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
		g.exec("ALTER TABLE providers DROP b", 0)
		g.run(fixture.Select("SELECT p.proname FROM pg_depend d JOIN pg_proc p ON d.refclassid='pg_proc'::regclass AND d.refobjid=p.oid WHERE d.classid='pg_attrdef'::regclass AND p.proname='generated_provider'", []string{"proname"}, []string{"NAME"}, nil))
		g.exec("DROP FUNCTION generated_provider(integer)", 0)
	})
}
