package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInheritanceMaintMixedLists(t *testing.T) {
	for _, action := range []string{"ADD b INT,INHERIT p", "INHERIT p,ADD b INT"} {
		t.Run(action, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE p(a INT,b INT)", 0)
				s.exec("CREATE TABLE c(a INT)", 0)
				s.exec("INSERT INTO c VALUES(2)", 1)
				s.exec("ALTER TABLE c "+action, 0)
				s.edges("c", "p")
				s.values("SELECT a,b FROM p", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), nil}})
				inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), true, int64(1)}, {"b", int64(2), true, int64(1)}})
			})
		})
	}
	t.Run("LateIncompatibleParent", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE bad(a BIGINT)", 0)
			s.exec("CREATE TABLE c(a INT)", 0)
			s.exec("INSERT INTO c VALUES(2)", 1)
			s.fail("ALTER TABLE c INHERIT p,INHERIT bad", fixture.Diagnostic{Code: "42804", Message: `child table "c" has different type for column "a"`})
			s.edges("c")
			inheritanceMaintOrdinals(s, "c", nil)
			inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), true, int64(0)}})
			s.values("SELECT a FROM c", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}})
			s.exec("ALTER TABLE c INHERIT p", 0)
			inheritanceMaintOrdinals(s, "c", [][]any{{"p", int64(1), false}})
		})
	})
	t.Run("RelationshipPassOrder", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE c(a INT)", 0)
			s.exec("ALTER TABLE c INHERIT p,NO INHERIT p", 0)
			s.edges("c")
			s.exec("ALTER TABLE c INHERIT p", 0)
			s.exec("ALTER TABLE ONLY c NO INHERIT p,INHERIT p", 0)
			inheritanceMaintOrdinals(s, "c", [][]any{{"p", int64(1), false}})
			s.fail("ALTER TABLE c INHERIT p,NO INHERIT p", fixture.Diagnostic{Code: "42P07", Message: `relation "p" would be inherited from more than once`})
			s.edges("c", "p")
		})
	})
	t.Run("DropTypeCheckBeforeMisc", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a BIGINT,CONSTRAINT ck CHECK(a>0))", 0)
			s.exec("CREATE TABLE c(extra INT,a INT,CONSTRAINT ck CHECK(a>1))", 0)
			s.exec("INSERT INTO c VALUES(8,2)", 1)
			s.exec("ALTER TABLE c INHERIT p,DROP COLUMN extra,ALTER a TYPE BIGINT,DROP CONSTRAINT ck,ADD CONSTRAINT ck CHECK(a>0)", 0)
			s.edges("c", "p")
			s.values("SELECT a FROM p", []string{"a"}, []string{"INT8"}, [][]any{{int64(2)}})
			inheritanceAlterFacts(s, "c", [][]any{{"ck", true, int64(1), false}})
		})
	})
	t.Run("DropBeforeDetachIsStillProtected", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE c() INHERITS(p)", 0)
			s.fail("ALTER TABLE c NO INHERIT p,DROP COLUMN a", fixture.Diagnostic{Code: "42P16", Message: `cannot drop inherited column "a"`})
			s.edges("c", "p")
			inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), false, int64(1)}})
		})
	})
}

func TestReferenceInheritanceMaintPropagation(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT DEFAULT 7)", 0)
		s.exec("CREATE TABLE c(a INT DEFAULT 9)", 0)
		s.exec("CREATE TABLE g() INHERITS(c)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(2)", 1)
		s.exec("INSERT INTO g VALUES(3)", 1)
		s.exec("ALTER TABLE c INHERIT p", 0)
		s.exec("ALTER TABLE p ADD flag INT DEFAULT 7,ADD CONSTRAINT flag_ck CHECK(flag>0)", 0)
		s.values("SELECT a,flag FROM p ORDER BY a", []string{"a", "flag"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(7)}, {int64(3), int64(7)}})
		s.fail("ALTER TABLE p ADD doomed INT DEFAULT 4,ADD CONSTRAINT late CHECK(a<3)", fixture.Diagnostic{Code: "23514", Message: `check constraint "late" of relation "g" is violated by some row`, Schema: "public", Table: "g", Constraint: "late"})
		for _, table := range []string{"p", "c", "g"} {
			s.values("SELECT attname FROM pg_attribute WHERE attrelid='"+table+"'::regclass::oid AND attname='doomed' AND NOT attisdropped", []string{"attname"}, []string{"NAME"}, nil)
			s.values("SELECT conname FROM pg_constraint WHERE conrelid='"+table+"'::regclass::oid AND conname='late'", []string{"conname"}, []string{"NAME"}, nil)
		}
		s.exec("ALTER TABLE c NO INHERIT p", 0)
		s.exec("ALTER TABLE p ADD later TEXT", 0)
		s.exec("ALTER TABLE p ALTER a SET DEFAULT 8", 0)
		s.exec("INSERT INTO c DEFAULT VALUES", 1)
		s.values("SELECT a,flag FROM c ORDER BY a", []string{"a", "flag"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(7)}, {int64(3), int64(7)}, {int64(9), int64(7)}})
		for _, table := range []string{"c", "g"} {
			s.values("SELECT attname FROM pg_attribute WHERE attrelid='"+table+"'::regclass::oid AND attname='later' AND NOT attisdropped", []string{"attname"}, []string{"NAME"}, nil)
		}
		s.fail("INSERT INTO c(a,flag) VALUES(4,0)", inheritanceCheckDiagnostic("c", "flag_ck", "4, 0"))
	})
	t.Run("MergeKeepsLocalRecipe", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE c(a INT,flag INT DEFAULT 9)", 0)
			s.exec("CREATE TABLE g() INHERITS(c)", 0)
			s.exec("ALTER TABLE c INHERIT p", 0)
			s.exec("INSERT INTO c(a) VALUES(2)", 1)
			s.exec("ALTER TABLE p ADD flag INT DEFAULT 7", 0)
			s.exec("INSERT INTO c(a) VALUES(3)", 1)
			s.values("SELECT a,flag FROM c ORDER BY a", []string{"a", "flag"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(9)}, {int64(3), int64(9)}})
		})
	})
}
