package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInheritanceAlterRetiredPrimaryType(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL)", 0)
		s.exec("CREATE TABLE c(a INT) INHERITS(p)", 0)
		s.exec("ALTER TABLE c ADD CONSTRAINT c_key PRIMARY KEY(a)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(2)", 1)
		nn := inheritanceAlterConstraintOID(s, "c", "nn")
		pk := inheritanceAlterConstraintOID(s, "c", "c_key")
		s.exec("ALTER TABLE p DROP COLUMN a", 0)
		inheritanceAlterFacts(s, "c", [][]any{{"c_key", true, int64(0), true}, {"nn", false, int64(1), false}})
		s.exec("ALTER TABLE c ALTER a TYPE BIGINT USING a+10", 0)
		inheritanceAlterFacts(s, "c", [][]any{{"c_key", true, int64(0), true}})
		if inheritanceAlterConstraintOID(s, "c", "c_key") == pk {
			s.t.Fatal("TYPE retained obsolete primary constraint identity")
		}
		s.run(fixture.Select("SELECT conname FROM pg_constraint WHERE oid=$1::oid", []string{"conname"}, []string{"NAME"}, nil, nn))
		s.values("SELECT a FROM ONLY c", []string{"a"}, []string{"INT8"}, [][]any{{int64(12)}})
		s.values("SELECT attnotnull,attislocal,attinhcount FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attname='a'", []string{"attnotnull", "attislocal", "attinhcount"}, []string{"BOOL", "BOOL", "INT2"}, [][]any{{true, true, int64(0)}})
		s.values("SELECT count(*) FROM p", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}})
		s.run(fixture.ImplicitError("INSERT INTO c VALUES(NULL)", inheritanceNullDiagnostic("c", "a", "null")))
		duplicate := fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "c_key"`}
		duplicate.Detail = "Key (a)=(12) already exists."
		duplicate.Schema, duplicate.Table, duplicate.Constraint = "public", "c", "c_key"
		s.run(fixture.ImplicitError("INSERT INTO c VALUES(12)", duplicate))
		s.values("SELECT a FROM ONLY c", []string{"a"}, []string{"INT8"}, [][]any{{int64(12)}})
	})
}
