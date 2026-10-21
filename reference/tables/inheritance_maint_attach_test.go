package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInheritanceMaintAttach(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT DEFAULT 7,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE c(extra TEXT,a INT DEFAULT 9,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE g() INHERITS(c)", 0)
		s.exec("INSERT INTO p VALUES($1)", 1, 1)
		s.exec("INSERT INTO c VALUES($1,$2)", 1, "child", 2)
		s.exec("INSERT INTO g VALUES($1,$2)", 1, "leaf", 3)
		ck := inheritanceAlterConstraintOID(s, "c", "ck")
		s.notice("ALTER TABLE c INHERIT p")
		if inheritanceAlterConstraintOID(s, "c", "ck") != ck {
			s.t.Fatal("attach replaced existing child CHECK identity")
		}
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
		s.values("SELECT a FROM ONLY p", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
		step := fixture.Select("SELECT * FROM c ORDER BY a", []string{"extra", "a"}, []string{"TEXT", "INT4"}, [][]any{{"child", int64(2)}, {"leaf", int64(3)}})
		step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.TableDDLColumnTypes(t, s.b.PG, columns, step.Columns, step.Types, []bool{true, true})
		}
		s.run(step)
		inheritanceAlterAttributes(s, "c", [][]any{{"extra", int64(1), true, int64(0)}, {"a", int64(2), true, int64(1)}})
		inheritanceAlterFacts(s, "c", [][]any{{"ck", true, int64(1), false}})
		inheritanceAlterAttributes(s, "g", [][]any{{"extra", int64(1), false, int64(1)}, {"a", int64(2), false, int64(1)}})
		s.edges("c", "p")
		s.edges("g", "c")
		s.exec("INSERT INTO c(extra) VALUES($1)", 1, "default")
		s.values("SELECT a FROM ONLY c ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(9)}})
		s.fail("INSERT INTO c VALUES('bad',0)", inheritanceCheckDiagnostic("c", "ck", "bad, 0"))
	})
	t.Run("GeneratedRecipesAndNNNames", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT CONSTRAINT pn NOT NULL,g INT GENERATED ALWAYS AS(a+1) STORED)", 0)
			s.exec("CREATE TABLE c(g INT GENERATED ALWAYS AS(a+2) STORED,a INT CONSTRAINT cn NOT NULL)", 0)
			nn := inheritanceAlterConstraintOID(s, "c", "cn")
			s.exec("INSERT INTO c(a) VALUES($1)", 1, 4)
			s.notice("ALTER TABLE c INHERIT p")
			if inheritanceAlterConstraintOID(s, "c", "cn") != nn {
				s.t.Fatal("attach replaced differently named child NOT NULL")
			}
			inheritanceAlterFacts(s, "c", [][]any{{"cn", true, int64(1), false}})
			s.values("SELECT g,a FROM c", []string{"g", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(6), int64(4)}})
			s.values("SELECT a,g FROM p", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(6)}})
			s.exec("INSERT INTO c(a) VALUES($1)", 1, 5)
			s.values("SELECT g FROM c ORDER BY a", []string{"g"}, []string{"INT4"}, [][]any{{int64(6)}, {int64(7)}})
		})
	})
}
