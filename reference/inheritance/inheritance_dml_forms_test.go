package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func inheritanceDMLRows(s *inheritanceCase, q string, names, types []string, rows [][]any) {
	step := fixture.Select(q, names, types, rows)
	step.Bag = true
	step.CheckMetadata = inheritanceDMLMetadata(s, names, types)
	s.run(step)
}

func inheritanceDMLMetadata(s *inheritanceCase, names, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, r *sql.Rows) {
		columns, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		nullable := make([]bool, len(names))
		for i := range nullable {
			nullable[i] = true
		}
		fixture.TableDDLColumnTypes(t, s.b.PG, columns, names, types, nullable)
	}
}

func TestReferenceInheritanceDMLForms(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceQueryMapped(s)
		inheritanceDMLRows(s, "UPDATE public.p AS r SET a=r.a+10 RETURNING r.a,r.x", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(11), "p"}, {int64(12), "c"}})
		s.values("SELECT * FROM ONLY c", []string{"y", "a", "x", "z"}, []string{"BOOL", "INT4", "TEXT", "INT8"}, [][]any{{true, int64(12), "c", int64(9)}})
		s.exec(`UPDATE ONLY("public"."p") r SET a=r.a+1`, 1)
		s.exec("UPDATE ONLY public.p SET a=a", 1)
		s.exec(`UPDATE "public"."p"* SET a=a`, 2)
		for _, predicate := range []string{"false", "NULL", "a=99"} {
			s.exec("UPDATE p SET a=0 WHERE "+predicate, 0)
			inheritanceDMLRows(s, "DELETE FROM p WHERE "+predicate+" RETURNING *", []string{"a", "x"}, []string{"INT4", "TEXT"}, nil)
		}
		for _, tc := range []struct{ q, code, message, token string }{
			{"UPDATE p SET z=0", "42703", `column "z" of relation "p" does not exist`, "z"},
			{"UPDATE p SET a=0 WHERE z=9", "42703", `column "z" does not exist`, "z"},
			{"DELETE FROM p RETURNING z", "42703", `column "z" does not exist`, "z"},
			{"UPDATE ONLY missing SET a=0", "42P01", `relation "missing" does not exist`, "missing"},
			{"UPDATE ONLY p* SET a=0", "42601", `syntax error at or near "*"`, "*"},
			{"DELETE FROM ONLY(p*)", "42601", `syntax error at or near "*"`, "*"},
			{"UPDATE p SET a=0; DELETE FROM ONLY p*", "42601", `syntax error at or near "*"`, "*"},
		} {
			step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fmt.Sprint(strings.Index(tc.q, tc.token) + 1)})
			if strings.Contains(tc.q, "RETURNING") {
				step.SelectRows = true
			}
			// A multi-command prepared statement is a separate incumbent protocol limit.
			if strings.Contains(tc.q, ";") {
				step.Prepared = false
				fixture.RunStep(s.t, s.p.Ctx, s.c, s.record, step)
			} else {
				s.run(step)
			}
		}
		s.values("SELECT a,x FROM p ORDER BY x", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(12), "c"}, {int64(12), "p"}})
		inheritanceDMLRows(s, "DELETE FROM ONLY(p) AS r RETURNING r.*", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(12), "p"}})
		s.values("SELECT a FROM c", []string{"a"}, []string{"INT4"}, [][]any{{int64(12)}})
		s.exec(`DELETE FROM "public"."p"* r`, 1)
		inheritanceDMLRows(s, "UPDATE p SET a=1 RETURNING *", []string{"a", "x"}, []string{"INT4", "TEXT"}, nil)
	})
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TEMP TABLE mine() INHERITS(p)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO mine VALUES(2)", 1)
		peer, err := s.b.DB.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer peer.Close()
		other := func(step fixture.Step) {
			step.Prepared = s.prepared
			fixture.RunStep(s.t, s.p.Ctx, peer, s.b.Recorder, step)
		}
		other(fixture.Exec("CREATE TEMP TABLE foreign_child() INHERITS(p)", 0))
		other(fixture.Exec("INSERT INTO foreign_child VALUES(90)", 1))
		s.exec("UPDATE p SET a=a+10", 2)
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(12)}})
		other(fixture.Select("SELECT a FROM ONLY foreign_child", []string{"a"}, []string{"INT4"}, [][]any{{int64(90)}}))
		s.exec("DELETE FROM p", 2)
		other(fixture.Exec("DROP TABLE foreign_child", 0))
		s.exec("DROP TABLE mine", 0)
	})
}

func inheritanceDMLDiamond(s *inheritanceCase) {
	s.exec("CREATE TABLE p(a INT,x TEXT)", 0)
	s.exec("CREATE TABLE q(y BOOL,a INT)", 0)
	s.exec("CREATE TABLE l() INHERITS(p)", 0)
	s.exec("CREATE TABLE r() INHERITS(p)", 0)
	s.exec("CREATE TABLE d(z BIGINT,CONSTRAINT d_limit CHECK(a<10)) INHERITS(q,l,r)", 0)
	s.exec("INSERT INTO p VALUES(7,'same'),(7,'same')", 2)
	s.exec("INSERT INTO l VALUES(7,'same')", 1)
	s.exec("INSERT INTO d VALUES(true,7,'same',9),(true,7,'same',9)", 2)
}

func inheritanceDMLFive(a int64) [][]any {
	return [][]any{{a, "same"}, {a, "same"}, {a, "same"}, {a, "same"}, {a, "same"}}
}

func TestReferenceInheritanceDMLDiamond(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceDMLDiamond(s)
		s.exec("UPDATE p SET a=a+1", 5)
		s.exec("UPDATE p SET a=a", 5)
		inheritanceDMLRows(s, "SELECT * FROM p", []string{"a", "x"}, []string{"INT4", "TEXT"}, inheritanceDMLFive(8))
		s.exec("UPDATE ONLY p SET a=9", 2)
		s.exec("DELETE FROM ONLY p WHERE a=8", 0)
		s.exec("DELETE FROM ONLY p", 2)
		s.exec("DELETE FROM p WHERE a=8", 3)
		s.exec("CREATE TABLE zero()", 0)
		s.exec("CREATE TABLE zero_child() INHERITS(zero)", 0)
		s.exec("INSERT INTO zero DEFAULT VALUES", 1)
		s.exec("INSERT INTO zero_child DEFAULT VALUES", 1)
		s.exec("INSERT INTO zero_child DEFAULT VALUES", 1)
		s.exec("DELETE FROM ONLY zero", 1)
		s.exec("DELETE FROM zero*", 2)
		s.values("SELECT count(*) AS n FROM zero", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}

func TestReferenceInheritanceDMLInsertPhysical(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT PRIMARY KEY,x TEXT DEFAULT 'parent')", 0)
		s.exec("CREATE TABLE c(z INT DEFAULT 9) INHERITS(p)", 0)
		s.exec("INSERT INTO p(a) VALUES(1)", 1)
		s.exec("INSERT INTO c(a) VALUES(2)", 1)
		inserted := fixture.Select("INSERT INTO p(a) VALUES(3) RETURNING *", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "parent"}})
		inserted.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.TableDDLColumnTypes(t, s.b.PG, columns, inserted.Columns, inserted.Types, []bool{false, true})
		}
		s.run(inserted)
		s.exec("INSERT INTO p(a) SELECT 4", 1)
		s.exec("INSERT INTO p(a) VALUES(2) ON CONFLICT(a) DO NOTHING", 1)
		s.exec("INSERT INTO p(a) VALUES(1) ON CONFLICT(a) DO NOTHING", 0)
		s.fail("INSERT INTO p(z) VALUES(8)", fixture.Diagnostic{Code: "42703", Message: `column "z" of relation "p" does not exist`, Position: "15"})
		s.values("SELECT * FROM ONLY p ORDER BY a", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "parent"}, {int64(2), "parent"}, {int64(3), "parent"}, {int64(4), "parent"}})
		s.values("SELECT * FROM ONLY c", []string{"a", "x", "z"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(2), "parent", int64(9)}})
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(4)}})
	})
}
