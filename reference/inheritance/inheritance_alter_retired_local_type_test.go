package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceAlterRetiredLocalNotNullType(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL)", 0)
		s.exec("CREATE TABLE c(a INT) INHERITS(p)", 0)
		old := inheritanceAlterConstraintOID(s, "c", "nn")
		s.exec("ALTER TABLE c ADD CONSTRAINT nn NOT NULL a", 0)
		inheritanceAlterFacts(s, "c", [][]any{{"nn", true, int64(1), false}})
		if inheritanceAlterConstraintOID(s, "c", "nn") != old {
			s.t.Fatal("localization replaced actual inherited NN")
		}
		s.exec("INSERT INTO c VALUES(3)", 1)
		s.exec("ALTER TABLE p DROP COLUMN a", 0)
		inheritanceAlterFacts(s, "c", [][]any{{"nn", true, int64(1), false}})
		s.exec("ALTER TABLE c ALTER a TYPE BIGINT USING a+10", 0)
		inheritanceAlterFacts(s, "c", [][]any{{"nn", true, int64(0), false}})
		if inheritanceAlterConstraintOID(s, "c", "nn") == old {
			s.t.Fatal("TYPE did not reconstruct actual local NN identity")
		}
		s.values("SELECT attislocal,attinhcount,attnotnull FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attname='a'", []string{"attislocal", "attinhcount", "attnotnull"}, []string{"BOOL", "INT2", "BOOL"}, [][]any{{true, int64(0), true}})
		s.values("SELECT a FROM c", []string{"a"}, []string{"INT8"}, [][]any{{int64(13)}})
		s.fail("INSERT INTO c VALUES(NULL)", inheritanceNullDiagnostic("c", "a", "null"))
	})
}

func TestReferenceInheritanceAlterRetiredLocalCheckType(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		// Root56 independently proves CHECK reconstruction expires its old
		// historical contributions, just as the genuine NN reconstruction does.
		s.exec("CREATE TABLE p(a INT,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE c(a INT) INHERITS(p)", 0)
		old := inheritanceAlterConstraintOID(s, "c", "ck")
		s.exec("ALTER TABLE c ADD CONSTRAINT ck CHECK(a>0)", 0)
		inheritanceAlterFacts(s, "c", [][]any{{"ck", true, int64(1), false}})
		s.exec("INSERT INTO c VALUES(3)", 1)
		s.exec("ALTER TABLE p DROP COLUMN a", 0)
		inheritanceAlterFacts(s, "c", [][]any{{"ck", true, int64(1), false}})
		s.exec("ALTER TABLE c ALTER a TYPE BIGINT USING a+10", 0)
		inheritanceAlterFacts(s, "c", [][]any{{"ck", true, int64(0), false}})
		if inheritanceAlterConstraintOID(s, "c", "ck") == old {
			s.t.Fatal("TYPE did not reconstruct actual local CHECK identity")
		}
		s.values("SELECT a FROM c", []string{"a"}, []string{"INT8"}, [][]any{{int64(13)}})
		s.fail("INSERT INTO c VALUES(0)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "c" violates check constraint "ck"`, Detail: "Failing row contains (0).", Schema: "public", Table: "c", Constraint: "ck"})
	})
}
