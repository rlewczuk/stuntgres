package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Public behavioral union migrated from the internal SQL-driven
// TestInheritanceAlterOnlyMixedActions/CheckProvenanceAtomicity witnesses.
func TestReferenceInheritanceMaintOnlyMixedMigration(t *testing.T) {
	t.Run("DropThenRootExpression", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT,b INT,g INT GENERATED ALWAYS AS(a+1) STORED)", 0)
			s.exec("CREATE TABLE c() INHERITS(p)", 0)
			s.exec("INSERT INTO p(a,b) VALUES(1,9)", 1)
			s.exec("INSERT INTO c(a,b) VALUES(2,8)", 1)
			var generation int64
			const definition = "SELECT d.oid::bigint FROM pg_attrdef d JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum WHERE d.adrelid='c'::regclass::oid AND a.attname='g'"
			if err := s.c.QueryRowContext(s.p.Ctx, definition).Scan(&generation); err != nil {
				s.t.Fatal(err)
			}
			s.exec("ALTER TABLE ONLY p DROP COLUMN b,ALTER g SET EXPRESSION AS(a+10)", 0)
			var after int64
			if err := s.c.QueryRowContext(s.p.Ctx, definition).Scan(&after); err != nil || after != generation {
				s.t.Fatal("ONLY root recipe changed child generation identity", after, generation, err)
			}
			inheritanceAlterAttributes(s, "p", [][]any{{"a", int64(1), true, int64(0)}, {"g", int64(3), true, int64(0)}})
			inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), false, int64(1)}, {"b", int64(2), true, int64(0)}, {"g", int64(3), false, int64(1)}})
			s.values("SELECT a,g FROM ONLY p", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}})
			s.values("SELECT a,b,g FROM ONLY c", []string{"a", "b", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(8), int64(3)}})
		})
	})
	t.Run("DropThenPhysicalUnique", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT,b INT)", 0)
			s.exec("CREATE TABLE c() INHERITS(p)", 0)
			s.exec("INSERT INTO p VALUES(1,9)", 1)
			s.exec("INSERT INTO c VALUES(2,8)", 1)
			s.exec("ALTER TABLE ONLY p DROP COLUMN b,ADD CONSTRAINT uq UNIQUE(a)", 0)
			s.constraints("p", [][]any{{"uq", "u"}})
			s.constraints("c", nil)
			s.fail("INSERT INTO p VALUES(1)", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "uq"`, Detail: "Key (a)=(1) already exists.", Schema: "public", Table: "p", Constraint: "uq"})
			s.exec("INSERT INTO c VALUES(1,7)", 1)
			s.values("SELECT a FROM ONLY p", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
			s.values("SELECT a,b FROM ONLY c ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(8)}})
		})
	})
	for _, kind := range []string{"NotNull", "Check"} {
		t.Run("OnlyDropAdd"+kind, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				declaration, addition := "a INT CONSTRAINT fact NOT NULL", "ADD CONSTRAINT replacement NOT NULL a"
				if kind == "Check" {
					declaration, addition = "a INT CONSTRAINT fact CHECK(a>0)", "ADD CONSTRAINT fact CHECK(a>1)"
				}
				s.exec("CREATE TABLE p("+declaration+")", 0)
				s.exec("CREATE TABLE c() INHERITS(p)", 0)
				s.exec("INSERT INTO p VALUES(2)", 1)
				s.exec("INSERT INTO c VALUES(3)", 1)
				parent, child := inheritanceAlterConstraintOID(s, "p", "fact"), inheritanceAlterConstraintOID(s, "c", "fact")
				s.fail("ALTER TABLE ONLY p DROP CONSTRAINT fact,"+addition, fixture.Diagnostic{Code: "42P16", Message: "constraint must be added to child tables too"})
				if kind == "Check" {
					q := "ALTER TABLE ONLY p DROP CONSTRAINT fact,ADD CONSTRAINT fact CHECK(missing>0)"
					s.fail(q, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`})
				}
				if inheritanceAlterConstraintOID(s, "p", "fact") != parent || inheritanceAlterConstraintOID(s, "c", "fact") != child {
					s.t.Fatal("failed mixed ONLY actions replaced fact identities")
				}
				inheritanceAlterFacts(s, "p", [][]any{{"fact", true, int64(0), false}})
				inheritanceAlterFacts(s, "c", [][]any{{"fact", false, int64(1), false}})
				s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
				s.edges("c", "p")
			})
		})
	}
	t.Run("CheckProvenanceAtomicity", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE c() INHERITS(p)", 0)
			s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
			s.exec("INSERT INTO p VALUES(2)", 1)
			s.exec("INSERT INTO c VALUES(3)", 1)
			s.exec("INSERT INTO leaf VALUES(4)", 1)
			s.exec("ALTER TABLE p ADD CONSTRAINT ck CHECK(a>0)", 0)
			ids := make(map[string]int64)
			for _, name := range []string{"p", "c", "leaf"} {
				ids[name] = inheritanceAlterConstraintOID(s, name, "ck")
			}
			s.fail("ALTER TABLE p DROP CONSTRAINT ck,ADD CONSTRAINT ck CHECK(a<4)", fixture.Diagnostic{Code: "23514", Message: `check constraint "ck" of relation "leaf" is violated by some row`, Schema: "public", Table: "leaf", Constraint: "ck"})
			for _, name := range []string{"p", "c", "leaf"} {
				if inheritanceAlterConstraintOID(s, name, "ck") != ids[name] {
					s.t.Fatal("failed replacement changed identity", name)
				}
			}
			s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(4)}})
			s.exec("ALTER TABLE p DROP CONSTRAINT ck,ADD CONSTRAINT ck CHECK(a>1)", 0)
			for _, name := range []string{"p", "c", "leaf"} {
				if inheritanceAlterConstraintOID(s, name, "ck") == ids[name] {
					s.t.Fatal("genuine replacement reused old fact identity", name)
				}
				count, local := int64(1), false
				if name == "p" {
					count, local = 0, true
				}
				inheritanceAlterFacts(s, name, [][]any{{"ck", local, count, false}})
			}
		})
	})
}
