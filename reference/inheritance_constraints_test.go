package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceInheritanceConstraints(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT CONSTRAINT first_nn NOT NULL DEFAULT 7,dead TEXT,b INT,CONSTRAINT ck CHECK(a>b),CONSTRAINT private_ck CHECK(b>100) NO INHERIT)", 0)
		s.exec("ALTER TABLE p DROP COLUMN dead", 0)
		s.exec("CREATE TABLE q(b INT,a INT CONSTRAINT second_nn NOT NULL DEFAULT 7,CONSTRAINT ck CHECK(a>b))", 0)
		s.notice("CREATE TABLE c(a INT NULL) INHERITS(p,q)", inheritanceMergeNotice("b"), inheritanceMergeNotice("a"), inheritanceLocalNotice("a", false))
		s.fail("INSERT INTO p VALUES(5,2)", inheritanceCheckDiagnostic("p", "private_ck", "5, 2"))
		s.constraints("c", [][]any{{"ck", "c"}, {"first_nn", "n"}})
		s.exec("INSERT INTO c VALUES(5,NULL),(6,2)", 2)
		want := referenceSelect("SELECT * FROM c ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(5), nil}, {int64(6), int64(2)}})
		s.run(want)
		referenceTableDDLMetadata(s.t, s.p.ctx, s.c, s.b.pg, s.prepared, "SELECT * FROM c", []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{false, true}, 2)
		s.fail("INSERT INTO c VALUES(NULL,1)", inheritanceNullDiagnostic("c", "a", "null, 1"))
		s.fail("INSERT INTO c VALUES(8,1),(1,2)", inheritanceCheckDiagnostic("c", "ck", "1, 2"))
		s.run(want)
		s.fail("UPDATE c SET b=a+1", inheritanceCheckDiagnostic("c", "ck", "5, 6"))
		s.run(want)
		s.fail("UPDATE c SET a=NULL WHERE a=5", inheritanceNullDiagnostic("c", "a", "null, null"))
		s.run(want)
		s.values("SELECT c.oid<>p.oid AS fresh FROM pg_constraint c JOIN pg_constraint p ON p.conname=c.conname JOIN pg_class ct ON ct.oid=c.conrelid JOIN pg_class pt ON pt.oid=p.conrelid WHERE ct.relname='c' AND pt.relname='p' ORDER BY c.conname", []string{"fresh"}, []string{"BOOL"}, [][]any{{true}, {true}})
		s.fail("ALTER TABLE c ALTER a DROP NOT NULL", referenceDiagnostic{Code: "42P16", Message: `cannot drop inherited constraint "first_nn" of relation "c"`})
		if s.b.pg {
			s.values("SELECT conname,conislocal,coninhcount FROM pg_constraint WHERE conrelid='c'::regclass ORDER BY conname", []string{"conname", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"ck", false, int64(2)}, {"first_nn", false, int64(2)}})
			s.exec("CREATE TABLE nn_noinherit(a INT NOT NULL NO INHERIT)", 0)
			s.exec("CREATE TABLE nn_nullable() INHERITS(nn_noinherit)", 0)
			s.exec("INSERT INTO nn_nullable VALUES(NULL)", 1)
			s.values("SELECT * FROM nn_nullable", []string{"a"}, []string{"INT4"}, [][]any{{nil}})
		} else {
			q := "CREATE TABLE nn_noinherit(a INT NOT NULL NO INHERIT)"
			s.fail(q, referenceDiagnostic{Code: "0A000", Message: "NOT NULL modifiers are unsupported", Position: referencePosition(strings.Index(q, "NO INHERIT") + 1)})
			s.absent("nn_noinherit")
		}
	})
}

func TestReferenceInheritanceLocalConstraints(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT CONSTRAINT inherited_nn NOT NULL,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE named(a INT CONSTRAINT local_nn NOT NULL,CONSTRAINT ck CHECK(a>0)) INHERITS(p)", 0)
		s.constraints("named", [][]any{{"ck", "c"}, {"local_nn", "n"}})
		s.exec("CREATE TABLE unnamed(a INT NOT NULL) INHERITS(p)", 0)
		s.constraints("unnamed", [][]any{{"ck", "c"}, {"unnamed_a_not_null", "n"}})
		s.exec("CREATE TABLE q(a INT,CONSTRAINT other_ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE distinct_checks() INHERITS(p,q)", 0)
		s.constraints("distinct_checks", [][]any{{"ck", "c"}, {"inherited_nn", "n"}, {"other_ck", "c"}})
		s.exec("CREATE TABLE collision_parent(a INT,CONSTRAINT collision_a_check CHECK(a>0))", 0)
		s.exec("CREATE TABLE collision(CHECK(a>0)) INHERITS(collision_parent)", 0)
		s.constraints("collision", [][]any{{"collision_a_check", "c"}, {"collision_a_check1", "c"}})
		if s.b.pg {
			s.values("SELECT conname,conislocal,coninhcount FROM pg_constraint WHERE conrelid='named'::regclass ORDER BY conname", []string{"conname", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"ck", true, int64(1)}, {"local_nn", true, int64(1)}})
		}
		for _, name := range []string{"named", "unnamed", "distinct_checks", "collision"} {
			s.exec("INSERT INTO "+name+" VALUES(5)", 1)
			s.values("SELECT * FROM "+name, []string{"a"}, []string{"INT4"}, [][]any{{int64(5)}})
		}
	})
}

func TestReferenceInheritanceNotNullNameCollision(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT CONSTRAINT same_name NOT NULL)", 0)
		s.exec("CREATE TABLE q(b INT CONSTRAINT same_name NOT NULL)", 0)
		s.exec("CREATE TABLE c() INHERITS(p,q)", 0)
		s.constraints("c", [][]any{{"c_b_not_null", "n"}, {"same_name", "n"}})
		s.fail("INSERT INTO c VALUES(1,NULL)", inheritanceNullDiagnostic("c", "b", "1, null"))
		s.exec("INSERT INTO c VALUES(1,2)", 1)
		s.values("SELECT * FROM c", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}})
	})
}
