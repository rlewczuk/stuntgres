package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func inheritanceMaintOrdinals(s *inheritanceCase, child string, rows [][]any) {
	s.values("SELECT p.relname,i.inhseqno,i.inhdetachpending FROM pg_inherits i JOIN pg_class p ON p.oid=i.inhparent WHERE i.inhrelid='"+child+"'::regclass::oid ORDER BY i.inhseqno", []string{"relname", "inhseqno", "inhdetachpending"}, []string{"NAME", "INT4", "BOOL"}, rows)
}

func TestReferenceInheritanceMaintDetach(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT DEFAULT 7 CONSTRAINT nn NOT NULL,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("CREATE TABLE g() INHERITS(c)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(2)", 1)
		s.exec("INSERT INTO g VALUES(3)", 1)
		nn, ck := inheritanceAlterConstraintOID(s, "c", "nn"), inheritanceAlterConstraintOID(s, "c", "ck")
		s.exec("ALTER TABLE ONLY c NO INHERIT p", 0)
		s.edges("c")
		s.edges("g", "c")
		inheritanceMaintOrdinals(s, "c", nil)
		inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), true, int64(0)}})
		inheritanceAlterAttributes(s, "g", [][]any{{"a", int64(1), false, int64(1)}})
		inheritanceAlterFacts(s, "c", [][]any{{"ck", true, int64(0), false}, {"nn", true, int64(0), false}})
		if inheritanceAlterConstraintOID(s, "c", "nn") != nn || inheritanceAlterConstraintOID(s, "c", "ck") != ck {
			s.t.Fatal("detach reminted child facts")
		}
		s.values("SELECT a FROM p", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
		s.values("SELECT a FROM c ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
		s.values("SELECT relhassubclass FROM pg_class WHERE oid='p'::regclass::oid", []string{"relhassubclass"}, []string{"BOOL"}, [][]any{{true}})
		s.fail("ALTER TABLE c NO INHERIT p", fixture.Diagnostic{Code: "42P01", Message: `relation "p" is not a parent of relation "c"`})
		s.exec("ALTER TABLE c * INHERIT p", 0)
		inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), true, int64(1)}})
		inheritanceAlterFacts(s, "c", [][]any{{"ck", true, int64(1), false}, {"nn", true, int64(1), false}})
		s.exec("ALTER TABLE c NO INHERIT p", 0)
		s.exec("DROP TABLE p", 0)
		s.exec("INSERT INTO c DEFAULT VALUES", 1)
		s.values("SELECT a FROM c ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(7)}})
		s.fail("INSERT INTO c VALUES(NULL)", inheritanceNullDiagnostic("c", "a", "null"))
		s.fail("INSERT INTO c VALUES(0)", inheritanceCheckDiagnostic("c", "ck", "0"))
	})
}

func TestReferenceInheritanceMaintDiamondOrdinals(t *testing.T) {
	t.Run("SparseLiveMaximum", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			for _, name := range []string{"p1", "p2", "p3", "p4", "p5", "p6", "c"} {
				s.exec("CREATE TABLE "+name+"(a INT)", 0)
			}
			for _, name := range []string{"p1", "p2", "p3"} {
				s.exec("ALTER TABLE c INHERIT "+name, 0)
			}
			inheritanceMaintOrdinals(s, "c", [][]any{{"p1", int64(1), false}, {"p2", int64(2), false}, {"p3", int64(3), false}})
			s.exec("ALTER TABLE c NO INHERIT p2", 0)
			inheritanceMaintOrdinals(s, "c", [][]any{{"p1", int64(1), false}, {"p3", int64(3), false}})
			s.fail("ALTER TABLE c INHERIT p1", fixture.Diagnostic{Code: "42P07", Message: `relation "p1" would be inherited from more than once`})
			s.exec("ALTER TABLE c INHERIT p4", 0)
			inheritanceMaintOrdinals(s, "c", [][]any{{"p1", int64(1), false}, {"p3", int64(3), false}, {"p4", int64(4), false}})
			s.exec("ALTER TABLE c NO INHERIT p4,INHERIT p5", 0)
			inheritanceMaintOrdinals(s, "c", [][]any{{"p1", int64(1), false}, {"p3", int64(3), false}, {"p5", int64(4), false}})
			s.exec("ALTER TABLE c NO INHERIT p1,NO INHERIT p3,NO INHERIT p5,INHERIT p6", 0)
			inheritanceMaintOrdinals(s, "c", [][]any{{"p6", int64(1), false}})
			inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), true, int64(1)}})
		})
	})
	t.Run("AlternativePathAndIndirectAncestor", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE l() INHERITS(p)", 0)
			s.exec("CREATE TABLE r() INHERITS(p)", 0)
			s.exec("CREATE TABLE d() INHERITS(l,r)", 0)
			s.exec("INSERT INTO p VALUES(1)", 1)
			s.exec("INSERT INTO l VALUES(2)", 1)
			s.exec("INSERT INTO r VALUES(3)", 1)
			s.exec("INSERT INTO d VALUES(4)", 1)
			s.exec("ALTER TABLE d INHERIT p", 0)
			inheritanceAlterAttributes(s, "d", [][]any{{"a", int64(1), false, int64(3)}})
			s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}})
			s.exec("ALTER TABLE l NO INHERIT p", 0)
			s.exec("ALTER TABLE d NO INHERIT p", 0)
			s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}, {int64(4)}})
			inheritanceAlterAttributes(s, "d", [][]any{{"a", int64(1), false, int64(2)}})
			s.exec("ALTER TABLE d NO INHERIT r", 0)
			s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}})
			s.values("SELECT a FROM l ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(4)}})
			inheritanceMaintOrdinals(s, "d", [][]any{{"l", int64(1), false}})
		})
	})
}
