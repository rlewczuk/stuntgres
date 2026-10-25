package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceDMLGenerated(t *testing.T) {
	for _, mode := range []string{"STORED", "VIRTUAL"} {
		t.Run(mode, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE p(a INT,g INT GENERATED ALWAYS AS(a+1) "+mode+",CONSTRAINT parent_only CHECK(a<3) NO INHERIT)", 0)
				s.exec("CREATE TABLE q(prefix BOOL,a INT)", 0)
				s.exec("CREATE TABLE c(g INT GENERATED ALWAYS AS(a+100) "+mode+",extra TEXT NOT NULL,CONSTRAINT c_positive CHECK(a>0),UNIQUE(a)) INHERITS(q,p)", 0)
				s.exec("INSERT INTO p(a) VALUES(1)", 1)
				s.exec("INSERT INTO c(prefix,a,extra) VALUES(true,2,'keep')", 1)
				childReturn := int64(103)
				if mode == "VIRTUAL" {
					childReturn = 4
				}
				inheritanceDMLRows(s, "UPDATE p SET a=a+1 RETURNING a,g", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}, {int64(3), childReturn}})
				s.values("SELECT * FROM ONLY c", []string{"prefix", "a", "g", "extra"}, []string{"BOOL", "INT4", "INT4", "TEXT"}, [][]any{{true, int64(3), int64(103), "keep"}})
				row := "0, 100"
				if mode == "VIRTUAL" {
					row = "0, virtual"
				}
				s.fail("UPDATE p SET a=0 WHERE a=3", inheritanceCheckDiagnostic("c", "c_positive", row))
				s.exec("INSERT INTO c(prefix,a,extra) VALUES(false,4,'other')", 1)
				s.fail("UPDATE p SET a=4 WHERE a=3", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "c_a_key"`, Detail: "Key (a)=(4) already exists.", Schema: "public", Table: "c", Constraint: "c_a_key"})
				s.values("SELECT a,g,extra FROM c ORDER BY a", []string{"a", "g", "extra"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(3), int64(103), "keep"}, {int64(4), int64(104), "other"}})
			})
		})
	}
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c(a INT NOT NULL) INHERITS(p)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(2)", 1)
		s.fail("UPDATE p SET a=NULL", inheritanceNullDiagnostic("c", "a", "null"))
		s.values("SELECT * FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
	})
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE referenced(a INT PRIMARY KEY)", 0)
		s.exec("INSERT INTO referenced VALUES(2)", 1)
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c(CONSTRAINT c_fk FOREIGN KEY(a) REFERENCES referenced(a)) INHERITS(p)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(2)", 1)
		s.fail("UPDATE p SET a=a+10", fixture.Diagnostic{Code: "23503", Message: `insert or update on table "c" violates foreign key constraint "c_fk"`, Detail: `Key (a)=(12) is not present in table "referenced".`, Schema: "public", Table: "c", Constraint: "c_fk"})
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
	})
}

func TestReferenceInheritanceDMLAtomicity(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c(extra TEXT,CONSTRAINT c_limit CHECK(a<10)) INHERITS(p)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(2,'keep')", 1)
		unchanged := func() {
			s.values("SELECT * FROM ONLY p", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
			s.values("SELECT * FROM c", []string{"a", "extra"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "keep"}})
		}
		s.fail("UPDATE p SET a=a+10", inheritanceCheckDiagnostic("c", "c_limit", "12"))
		unchanged()
		s.fail("UPDATE p SET a=10/(2-a)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		unchanged()
		for _, q := range []string{"UPDATE p SET a=a+1 RETURNING 10/(a-3)", "DELETE FROM p RETURNING 10/(a-2)"} {
			step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			step.SelectRows = true
			step.ErrorStage = "execute"
			s.run(step)
			unchanged()
		}
		s.exec("BEGIN", 0)
		s.exec("UPDATE p SET a=a+1", 2)
		s.exec("SAVEPOINT accepted", 0)
		s.fail("UPDATE p SET a=a+10", inheritanceCheckDiagnostic("c", "c_limit", "13"))
		s.fail("DELETE FROM p", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		s.exec("ROLLBACK TO accepted", 0)
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
		s.exec("DELETE FROM p", 2)
		s.exec("ROLLBACK TO accepted", 0)
		s.exec("COMMIT", 0)
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
		s.exec("BEGIN", 0)
		s.exec("UPDATE p SET a=a+1", 2)
		s.fail("UPDATE p SET a=99", inheritanceCheckDiagnostic("c", "c_limit", "99"))
		s.exec("ROLLBACK", 0)
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
		s.exec("ALTER TABLE c ADD CONSTRAINT c_pk PRIMARY KEY(a)", 0)
		s.exec("CREATE TABLE dependent(a INT CONSTRAINT dependent_fk REFERENCES c(a))", 0)
		s.exec("INSERT INTO dependent VALUES(3)", 1)
		s.fail("DELETE FROM p", fixture.Diagnostic{Code: "23503", Message: `update or delete on table "c" violates foreign key constraint "dependent_fk" on table "dependent"`, Detail: `Key (a)=(3) is still referenced from table "dependent".`, Schema: "public", Table: "dependent", Constraint: "dependent_fk"})
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
		s.exec("DELETE FROM dependent", 1)
		s.exec("DELETE FROM p", 2)
	})
}

func TestReferenceInheritanceDMLConsumers(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceQueryMapped(s)
		s.exec("CREATE TABLE delta(k INT,d INT)", 0)
		s.exec("INSERT INTO delta VALUES(1,10),(2,20)", 2)
		inheritanceDMLRows(s, "UPDATE p AS target SET a=target.a+delta.d FROM delta WHERE target.a=delta.k RETURNING WITH (OLD AS o,NEW AS n) o.a AS before,n.a AS after,target.x", []string{"before", "after", "x"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(11), "p"}, {int64(2), int64(22), "c"}})
		s.values("SELECT * FROM c", []string{"y", "a", "x", "z"}, []string{"BOOL", "INT4", "TEXT", "INT8"}, [][]any{{true, int64(22), "c", int64(9)}})
		inheritanceDMLRows(s, "UPDATE p SET a=a+(SELECT count(*)::int FROM delta) RETURNING a,x", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(13), "p"}, {int64(24), "c"}})
		s.exec("UPDATE delta SET k=k+d+2", 2)
		inheritanceDMLRows(s, "DELETE FROM p AS target USING delta WHERE target.a=delta.k RETURNING WITH (OLD AS o,NEW AS n) o.a,n.a AS gone,target.x", []string{"a", "gone", "x"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(13), nil, "p"}, {int64(24), nil, "c"}})
		s.values("SELECT * FROM p", []string{"a", "x"}, []string{"INT4", "TEXT"}, nil)
	})
}

func TestReferenceInheritanceDMLCTE(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceQueryMapped(s)
		s.values("WITH changed AS (UPDATE p SET a=a+10 RETURNING a,x) SELECT * FROM changed ORDER BY a", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(11), "p"}, {int64(12), "c"}})
		s.exec("CREATE TABLE sink(a INT)", 0)
		s.exec("WITH changed AS (UPDATE p SET a=a+1 RETURNING a) INSERT INTO sink SELECT a FROM changed", 2)
		s.values("SELECT * FROM sink ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(12)}, {int64(13)}})
		s.exec("WITH gone AS (DELETE FROM p WHERE a=12 RETURNING a) UPDATE p SET a=a+1 WHERE a=13", 1)
		s.values("SELECT a FROM p", []string{"a"}, []string{"INT4"}, [][]any{{int64(14)}})
		q := "WITH changed AS (UPDATE p SET a=a+1 RETURNING a) INSERT INTO sink SELECT 1/(a-15) FROM changed"
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		step.ErrorStage = "execute"
		s.run(step)
		s.values("SELECT a FROM p", []string{"a"}, []string{"INT4"}, [][]any{{int64(14)}})
		s.values("SELECT * FROM sink ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(12)}, {int64(13)}})
		s.values("WITH gone AS (DELETE FROM p RETURNING a,x) SELECT * FROM gone", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(14), "c"}})
	})
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("CREATE TABLE other(a INT)", 0)
		s.exec("CREATE TABLE other_child() INHERITS(other)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(2)", 1)
		s.exec("INSERT INTO other VALUES(10)", 1)
		s.exec("INSERT INTO other_child VALUES(20)", 1)
		q := "WITH first AS (UPDATE p SET a=a+10 RETURNING old.a AS key) UPDATE other SET a=other.a/(first.key-2) FROM first WHERE other.a=first.key*10"
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		step.ErrorStage = "execute"
		s.run(step)
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		s.values("SELECT a FROM other ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(10)}, {int64(20)}})
		s.exec("WITH first AS (UPDATE p SET a=a+10 RETURNING old.a AS key) UPDATE other SET a=other.a+1 FROM first WHERE other.a=first.key*10", 2)
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(12)}})
		s.values("SELECT a FROM other ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(21)}})
	})
}

func TestReferenceInheritanceDMLCurrentOf(t *testing.T) {
	for _, marked := range []bool{false, true} {
		t.Run(map[bool]string{false: "simple", true: "marked"}[marked], func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				inheritanceQueryMapped(s)
				s.exec("BEGIN", 0)
				defer s.c.ExecContext(s.p.Ctx, "ROLLBACK")
				suffix := ""
				if marked {
					suffix = " FOR UPDATE"
				}
				s.exec("DECLARE positioned CURSOR FOR SELECT a FROM p WHERE a=2"+suffix, 0)
				s.values("FETCH 1 FROM positioned", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}})
				s.exec("UPDATE ONLY p SET a=99 WHERE CURRENT OF positioned", 0)
				inheritanceDMLRows(s, "UPDATE p SET a=a+1 WHERE CURRENT OF positioned RETURNING a,x", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "c"}})
				s.values("SELECT a FROM ONLY p", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
				s.exec("DELETE FROM ONLY(p) WHERE CURRENT OF positioned", 0)
				inheritanceDMLRows(s, "DELETE FROM p WHERE CURRENT OF positioned RETURNING *", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "c"}})
				s.exec("DELETE FROM p WHERE CURRENT OF positioned", 0)
				s.exec("ROLLBACK", 0)
				for _, tc := range []struct{ decl, code, message string }{
					{"", "34000", `cursor "bad" does not exist`},
					{"DECLARE bad CURSOR FOR SELECT a FROM p", "24000", `cursor "bad" is not positioned on a row`},
					{"DECLARE bad CURSOR FOR SELECT p.a FROM p JOIN p again ON p.a=again.a FOR UPDATE", "24000", `cursor "bad" has multiple FOR UPDATE/SHARE references to table "p"`},
				} {
					s.exec("BEGIN", 0)
					if tc.decl != "" {
						s.exec(tc.decl, 0)
					}
					if tc.code == "24000" && strings.Contains(tc.decl, "JOIN") {
						s.values("FETCH 1 FROM bad", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
					}
					s.fail("DELETE FROM p WHERE CURRENT OF bad", fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.exec("ROLLBACK", 0)
				}
			})
		})
	}
}
