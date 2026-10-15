package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func inheritanceAlterConstraintOID(s *inheritanceCase, table, name string) int64 {
	var id int64
	if err := s.c.QueryRowContext(s.p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conrelid='"+table+"'::regclass::oid AND conname='"+name+"'").Scan(&id); err != nil {
		s.t.Fatal(err)
	}
	return id
}

func TestReferenceInheritanceAlterRename(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE c(extra TEXT) INHERITS(p)", 0)
		s.exec("CREATE TABLE g() INHERITS(c)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(2,'keep')", 1)
		ids := map[string]int64{}
		for _, table := range []string{"p", "c", "g"} {
			ids[table] = inheritanceAlterConstraintOID(s, table, "ck")
		}
		s.fail("ALTER TABLE ONLY p RENAME COLUMN a TO renamed", fixture.Diagnostic{Code: "42P16", Message: `inherited column "a" must be renamed in child tables too`})
		s.fail("ALTER TABLE c RENAME COLUMN a TO renamed", fixture.Diagnostic{Code: "42P16", Message: `cannot rename inherited column "a"`})
		s.exec("ALTER TABLE p RENAME COLUMN a TO renamed", 0)
		inheritanceAlterAttributes(s, "c", [][]any{{"renamed", int64(1), false, int64(1)}, {"extra", int64(2), true, int64(0)}})
		s.values("SELECT * FROM c", []string{"renamed", "extra"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "keep"}})
		for _, name := range []string{"ck", "nn"} {
			before := map[string]int64{}
			for _, table := range []string{"p", "c", "g"} {
				before[table] = inheritanceAlterConstraintOID(s, table, name)
			}
			s.fail("ALTER TABLE ONLY p RENAME CONSTRAINT "+name+" TO renamed_"+name, fixture.Diagnostic{Code: "42P16", Message: `inherited constraint "` + name + `" must be renamed in child tables too`})
			s.exec("ALTER TABLE p RENAME CONSTRAINT "+name+" TO renamed_"+name, 0)
			for _, table := range []string{"p", "c", "g"} {
				if id := inheritanceAlterConstraintOID(s, table, "renamed_"+name); id != before[table] {
					s.t.Fatal("rename changed physical OID", table, id, before[table])
				}
			}
		}
		if ids["c"] != inheritanceAlterConstraintOID(s, "c", "renamed_ck") {
			s.t.Fatal("column rename changed check OID")
		}
		inheritanceAlterFacts(s, "c", [][]any{{"renamed_ck", false, int64(1), false}, {"renamed_nn", false, int64(1), false}})
		s.exec("ALTER TABLE p ADD CONSTRAINT physical CHECK(renamed<100) NO INHERIT", 0)
		s.exec("ALTER TABLE ONLY p RENAME CONSTRAINT physical TO renamed_physical", 0)
		inheritanceAlterFacts(s, "g", [][]any{{"renamed_ck", false, int64(1), false}, {"renamed_nn", false, int64(1), false}})
	})
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceQueryMapped(s)
		s.fail("ALTER TABLE p RENAME a TO renamed", fixture.Diagnostic{Code: "42P16", Message: `cannot rename inherited column "a"`})
		s.values("SELECT * FROM p ORDER BY a", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "p"}, {int64(2), "c"}})
	})
}

func TestReferenceInheritanceAlterGenerated(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT,g INT GENERATED ALWAYS AS(a+1) STORED)", 0)
		s.exec("CREATE TABLE q(prefix BOOL)", 0)
		s.exec("CREATE TABLE c(g INT GENERATED ALWAYS AS(a+100) STORED,extra TEXT) INHERITS(q,p)", 0)
		s.exec("INSERT INTO p(a) VALUES(1)", 1)
		s.exec("INSERT INTO c(prefix,a,extra) VALUES(true,2,'keep')", 1)
		s.exec("ALTER TABLE p ALTER g SET EXPRESSION AS(a+2)", 0)
		s.values("SELECT a,g FROM p ORDER BY a", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}, {int64(2), int64(4)}})
		s.exec("ALTER TABLE ONLY p ALTER g SET EXPRESSION AS(a+3)", 0)
		s.values("SELECT a,g FROM p ORDER BY a", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(4)}, {int64(2), int64(4)}})
		s.exec("ALTER TABLE c ALTER g SET EXPRESSION AS(a+100)", 0)
		s.values("SELECT * FROM c", []string{"prefix", "a", "g", "extra"}, []string{"BOOL", "INT4", "INT4", "TEXT"}, [][]any{{true, int64(2), int64(102), "keep"}})
		s.fail("ALTER TABLE ONLY p ALTER g DROP EXPRESSION", fixture.Diagnostic{Code: "0A000", Message: "ALTER TABLE / DROP EXPRESSION must be applied to child tables too"})
		s.fail("ALTER TABLE c ALTER g DROP EXPRESSION", fixture.Diagnostic{Code: "42P16", Message: "cannot drop generation expression from inherited column"})
		s.exec("ALTER TABLE p ALTER g DROP EXPRESSION", 0)
		s.exec("UPDATE p SET a=a+1", 2)
		s.values("SELECT a,g FROM p ORDER BY a", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}, {int64(3), int64(102)}})
	})
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT,g INT GENERATED ALWAYS AS(a+1) VIRTUAL)", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("INSERT INTO c(a) VALUES(2)", 1)
		s.exec("ALTER TABLE p ALTER g SET EXPRESSION AS(a+2)", 0)
		s.values("SELECT a,g FROM c", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}})
		s.exec("ALTER TABLE p ADD CONSTRAINT ck CHECK(a>0)", 0)
		s.fail("ALTER TABLE p ALTER g SET EXPRESSION AS(a+3)", fixture.Diagnostic{Code: "0A000", Message: "ALTER TABLE / SET EXPRESSION is not supported for virtual generated columns in tables with check constraints", Detail: `Column "g" of relation "p" is a virtual generated column.`})
	})
}

func TestReferenceInheritanceAlterAtomicity(t *testing.T) {
	for _, failure := range []string{"add_type", "cast", "check", "null", "outside_parent"} {
		t.Run(failure, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE p(a INT,x TEXT)", 0)
				childDecl := ""
				if failure == "add_type" {
					childDecl = "flag TEXT"
				}
				s.exec("CREATE TABLE c("+childDecl+") INHERITS(p)", 0)
				s.exec("INSERT INTO p(a,x) VALUES(1,'1')", 1)
				s.exec("INSERT INTO c(a,x) VALUES(2,'bad')", 1)
				switch failure {
				case "add_type":
					s.fail("ALTER TABLE p ADD COLUMN added INT, ADD COLUMN flag BOOL DEFAULT true", fixture.Diagnostic{Code: "42804", Message: `child table "c" has different type for column "flag"`})
				case "cast":
					s.fail("ALTER TABLE p ADD COLUMN added INT, ALTER x TYPE INT USING x::int", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
				case "check":
					s.fail("ALTER TABLE p ADD COLUMN added INT, ADD CONSTRAINT small CHECK(a<2)", fixture.Diagnostic{Code: "23514", Message: `check constraint "small" of relation "c" is violated by some row`, Schema: "public", Table: "c", Constraint: "small"})
				case "null":
					s.exec("UPDATE c SET a=NULL", 1)
					s.fail("ALTER TABLE p ADD COLUMN added INT, ALTER a SET NOT NULL", fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "c" contains null values`, Schema: "public", Table: "c", Column: "a"})
				case "outside_parent":
					s.exec("CREATE TABLE other(a INT)", 0)
					s.exec("CREATE TABLE shared() INHERITS(p,other)", 0)
					s.fail("ALTER TABLE p ALTER a TYPE BIGINT", fixture.Diagnostic{Code: "42P16", Message: `cannot alter inherited column "a" of relation "shared"`})
				}
				inheritanceAlterAttributes(s, "p", [][]any{{"a", int64(1), true, int64(0)}, {"x", int64(2), true, int64(0)}})
				s.values("SELECT * FROM ONLY p", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1"}})
				a := any(int64(2))
				if failure == "null" {
					a = nil
				}
				s.values("SELECT a,x FROM ONLY c", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{a, "bad"}})
				inheritanceAlterFacts(s, "p", nil)
				inheritanceAlterFacts(s, "c", nil)
			})
		})
	}
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceQueryRole(s, "inherit_alter_parent_owner", "NOBYPASSRLS")
		inheritanceQueryRole(s, "inherit_alter_child_owner", "NOBYPASSRLS")
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("ALTER TABLE p OWNER TO inherit_alter_parent_owner", 0)
		s.exec("ALTER TABLE c OWNER TO inherit_alter_child_owner", 0)
		s.exec("SET ROLE inherit_alter_parent_owner", 0)
		s.fail("ALTER TABLE p ADD COLUMN b INT DEFAULT 7", fixture.Diagnostic{Code: "42501", Message: "must be owner of table c"})
		s.exec("SET ROLE NONE", 0)
		inheritanceAlterAttributes(s, "p", [][]any{{"a", int64(1), true, int64(0)}})
		inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), false, int64(1)}})
		s.exec("INSERT INTO c VALUES(2)", 1)
		s.values("SELECT * FROM p", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}})
	})
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c(CONSTRAINT c_unique UNIQUE(a)) INHERITS(p)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(2),(4)", 2)
		constraint := inheritanceAlterConstraintOID(s, "c", "c_unique")
		var index int64
		if err := s.c.QueryRowContext(s.p.Ctx, "SELECT conindid::bigint FROM pg_constraint WHERE conrelid='c'::regclass::oid AND conname='c_unique'").Scan(&index); err != nil {
			s.t.Fatal(err)
		}
		s.fail("ALTER TABLE p ALTER a TYPE BIGINT USING a%2", fixture.Diagnostic{Code: "23505", Message: `could not create unique index "c_unique"`, Detail: "Key (a)=(0) is duplicated.", Schema: "public", Table: "c", Constraint: "c_unique"})
		if inheritanceAlterConstraintOID(s, "c", "c_unique") != constraint {
			s.t.Fatal("failed rewrite changed constraint identity")
		}
		s.values("SELECT conindid::bigint AS backing FROM pg_constraint WHERE conrelid='c'::regclass::oid AND conname='c_unique'", []string{"backing"}, []string{"INT8"}, [][]any{{index}})
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(4)}})
	})
}

func TestReferenceInheritanceAlterQualifiedUsing(t *testing.T) {
	for _, qualifier := range []string{"p", "public.p"} {
		t.Run(qualifier, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE p(a INT)", 0)
				s.exec("INSERT INTO p VALUES(1)", 1)
				// PG18 accepts the named qualifier before descendants exist.
				s.exec("ALTER TABLE p ALTER a TYPE BIGINT USING "+qualifier+".a+10", 0)
				s.values("SELECT a FROM ONLY p", []string{"a"}, []string{"INT8"}, [][]any{{int64(11)}})
				s.exec("ALTER TABLE p ALTER a TYPE INT USING a-10", 0)
				s.values("SELECT a FROM ONLY p", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
				s.exec("CREATE TABLE q(prefix TEXT)", 0)
				// q precedes p, so the descendant's a has a different physical slot.
				s.exec("CREATE TABLE c(extra TEXT) INHERITS(q,p)", 0)
				s.exec("CREATE TABLE g() INHERITS(c)", 0)
				s.exec("INSERT INTO q VALUES('independent')", 1)
				s.exec("INSERT INTO c(prefix,a,extra) VALUES('child',70000,'keep'),('null',NULL,'nullable')", 2)
				s.exec("INSERT INTO g(prefix,a,extra) VALUES('leaf',3,'leaf-keep')", 1)
				// The same qualified expression is rejected during PG18 recursion.
				missing := fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "p"`, Position: "41"}
				s.fail("ALTER TABLE p ALTER a TYPE BIGINT USING "+qualifier+".a+10", missing)
				// The parent fits SMALLINT; a physical child fails during rewriting.
				s.fail("ALTER TABLE p ALTER a TYPE SMALLINT USING a", fixture.Diagnostic{Code: "22003", Message: "smallint out of range"})
				s.values("SELECT a FROM ONLY p", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
				s.values("SELECT * FROM ONLY c ORDER BY prefix", []string{"prefix", "a", "extra"}, []string{"TEXT", "INT4", "TEXT"}, [][]any{{"child", int64(70000), "keep"}, {"null", nil, "nullable"}})
				s.values("SELECT * FROM ONLY g", []string{"prefix", "a", "extra"}, []string{"TEXT", "INT4", "TEXT"}, [][]any{{"leaf", int64(3), "leaf-keep"}})
				s.exec("ALTER TABLE p ALTER a TYPE BIGINT USING a+10", 0)
				s.values("SELECT a FROM ONLY p", []string{"a"}, []string{"INT8"}, [][]any{{int64(11)}})
				s.values("SELECT * FROM ONLY c ORDER BY prefix", []string{"prefix", "a", "extra"}, []string{"TEXT", "INT8", "TEXT"}, [][]any{{"child", int64(70010), "keep"}, {"null", nil, "nullable"}})
				s.values("SELECT * FROM ONLY g", []string{"prefix", "a", "extra"}, []string{"TEXT", "INT8", "TEXT"}, [][]any{{"leaf", int64(13), "leaf-keep"}})
				s.values("SELECT * FROM ONLY q", []string{"prefix"}, []string{"TEXT"}, [][]any{{"independent"}})
			})
		})
	}
}
