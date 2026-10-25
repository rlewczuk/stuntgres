package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func inheritanceAlterAttributes(s *inheritanceCase, table string, rows [][]any) {
	s.values("SELECT attname,attnum,attislocal,attinhcount FROM pg_attribute WHERE attrelid='"+table+"'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum", "attislocal", "attinhcount"}, []string{"NAME", "INT2", "BOOL", "INT2"}, rows)
}

func inheritanceAlterFacts(s *inheritanceCase, table string, rows [][]any) {
	s.values("SELECT conname,conislocal,coninhcount,connoinherit FROM pg_constraint WHERE conrelid='"+table+"'::regclass::oid ORDER BY conname", []string{"conname", "conislocal", "coninhcount", "connoinherit"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, rows)
}

func TestReferenceInheritanceAlterColumns(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceDMLDiamond(s)
		s.exec("ALTER TABLE p ADD COLUMN flag INT DEFAULT 7 NOT NULL CHECK(flag>0)", 0)
		s.values("SELECT count(*) AS n,min(flag) AS low,max(flag) AS high FROM p", []string{"n", "low", "high"}, []string{"INT8", "INT4", "INT4"}, [][]any{{int64(5), int64(7), int64(7)}})
		inheritanceAlterAttributes(s, "d", [][]any{{"y", int64(1), false, int64(1)}, {"a", int64(2), false, int64(3)}, {"x", int64(3), false, int64(2)}, {"z", int64(4), true, int64(0)}, {"flag", int64(5), false, int64(2)}})
		s.fail("ALTER TABLE ONLY p ADD COLUMN forbidden INT", fixture.Diagnostic{Code: "42P16", Message: "column must be added to child tables too"})
		// q also supplies a, so mutate x instead for this diamond's type rewrite.
		s.exec("ALTER TABLE p ALTER x TYPE VARCHAR(12) USING x||'!'", 0)
		step := fixture.Select("SELECT x,flag FROM p", []string{"x", "flag"}, []string{"VARCHAR", "INT4"}, [][]any{{"same!", int64(7)}, {"same!", int64(7)}, {"same!", int64(7)}, {"same!", int64(7)}, {"same!", int64(7)}})
		step.Bag = true
		step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			// Native knows flag's NOT NULL declaration; pq exposes no nullability
			// information. x remains independently nullable on both native layouts.
			fixture.TableDDLColumnTypes(t, s.b.PG, columns, step.Columns, step.Types, []bool{true, false})
		}
		s.run(step)
		s.fail("ALTER TABLE ONLY p ALTER flag TYPE BIGINT", fixture.Diagnostic{Code: "42P16", Message: `type of inherited column "flag" must be changed in child tables too`})
	})
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE q(y BOOL)", 0)
		s.exec("CREATE TABLE p(a INT,x TEXT)", 0)
		s.exec("CREATE TABLE c(z BIGINT) INHERITS(q,p)", 0)
		s.exec("INSERT INTO p VALUES(1,'p')", 1)
		s.exec("INSERT INTO c VALUES(true,2,'c',9)", 1)
		s.exec("ALTER TABLE p ALTER a TYPE BIGINT USING a+10", 0)
		s.values("SELECT * FROM p ORDER BY a", []string{"a", "x"}, []string{"INT8", "TEXT"}, [][]any{{int64(11), "p"}, {int64(12), "c"}})
		s.values("SELECT * FROM c", []string{"y", "a", "x", "z"}, []string{"BOOL", "INT8", "TEXT", "INT8"}, [][]any{{true, int64(12), "c", int64(9)}})
	})
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c(flag INT DEFAULT 9) INHERITS(p)", 0)
		s.exec("INSERT INTO c VALUES(2,19)", 1)
		s.exec("ALTER TABLE p ADD COLUMN flag INT DEFAULT 7", 0)
		s.values("SELECT * FROM c", []string{"a", "flag"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(19)}})
		inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), false, int64(1)}, {"flag", int64(2), true, int64(1)}})
		s.exec("INSERT INTO c(a) VALUES(3)", 1)
		s.values("SELECT * FROM c ORDER BY a", []string{"a", "flag"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(19)}, {int64(3), int64(9)}})
	})
}

func TestReferenceInheritanceAlterOrigins(t *testing.T) {
	for _, only := range []bool{false, true} {
		t.Run(map[bool]string{false: "recursive", true: "only"}[only], func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE p(a INT,b TEXT)", 0)
				s.exec("CREATE TABLE c() INHERITS(p)", 0)
				s.exec("CREATE TABLE g() INHERITS(c)", 0)
				s.exec("CREATE TABLE local(a INT) INHERITS(p)", 0)
				s.exec("CREATE TABLE other(a INT)", 0)
				s.exec("CREATE TABLE shared() INHERITS(p,other)", 0)
				s.exec("INSERT INTO c VALUES(2,'c')", 1)
				s.exec("INSERT INTO g VALUES(3,'g')", 1)
				s.exec("INSERT INTO local VALUES(4,'local')", 1)
				s.exec("INSERT INTO shared VALUES(5,'shared')", 1)
				target := "p"
				if only {
					target = "ONLY p"
				}
				s.exec("ALTER TABLE "+target+" DROP COLUMN a", 0)
				inheritanceAlterAttributes(s, "p", [][]any{{"b", int64(2), true, int64(0)}})
				if only {
					inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), true, int64(0)}, {"b", int64(2), false, int64(1)}})
					inheritanceAlterAttributes(s, "g", [][]any{{"a", int64(1), false, int64(1)}, {"b", int64(2), false, int64(1)}})
					s.values("SELECT * FROM c ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "c"}, {int64(3), "g"}})
				} else {
					inheritanceAlterAttributes(s, "c", [][]any{{"b", int64(2), false, int64(1)}})
					inheritanceAlterAttributes(s, "g", [][]any{{"b", int64(2), false, int64(1)}})
					s.values("SELECT * FROM c ORDER BY b", []string{"b"}, []string{"TEXT"}, [][]any{{"c"}, {"g"}})
				}
				inheritanceAlterAttributes(s, "local", [][]any{{"a", int64(1), true, int64(0)}, {"b", int64(2), false, int64(1)}})
				inheritanceAlterAttributes(s, "shared", [][]any{{"a", int64(1), only, int64(1)}, {"b", int64(2), false, int64(1)}})
				s.values("SELECT a FROM local", []string{"a"}, []string{"INT4"}, [][]any{{int64(4)}})
				s.values("SELECT a FROM shared", []string{"a"}, []string{"INT4"}, [][]any{{int64(5)}})
				// A real dependency requires CASCADE, descendants alone do not.
				s.exec("CREATE VIEW retained AS SELECT b FROM p", 0)
				s.fail("ALTER TABLE p DROP COLUMN b", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop desired object(s) because other objects depend on them", Detail: "view retained depends on column b of table p", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				s.exec("ALTER TABLE p DROP COLUMN b CASCADE", 0)
				s.absent("retained")
			})
		})
	}
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE l() INHERITS(p)", 0)
		s.exec("CREATE TABLE r() INHERITS(p)", 0)
		s.exec("CREATE TABLE d() INHERITS(l,r)", 0)
		s.exec("INSERT INTO d VALUES(7)", 1)
		s.fail("ALTER TABLE l DROP COLUMN a", fixture.Diagnostic{Code: "42P16", Message: `cannot drop inherited column "a"`})
		s.exec("ALTER TABLE ONLY p DROP COLUMN a", 0)
		s.exec("ALTER TABLE l DROP COLUMN a", 0)
		inheritanceAlterAttributes(s, "d", [][]any{{"a", int64(1), false, int64(1)}})
		s.values("SELECT a FROM d", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
	})
}

func TestReferenceInheritanceAlterDefaults(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c(a INT DEFAULT 9) INHERITS(p)", 0)
		s.exec("CREATE TABLE g() INHERITS(c)", 0)
		s.exec("ALTER TABLE p ALTER a DROP DEFAULT", 0)
		s.exec("INSERT INTO c DEFAULT VALUES", 1)
		s.exec("INSERT INTO g DEFAULT VALUES", 1)
		s.values("SELECT a FROM c", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {nil}})
		s.exec("ALTER TABLE c ALTER a SET DEFAULT 6", 0)
		s.exec("INSERT INTO g DEFAULT VALUES", 1)
		s.values("SELECT a FROM ONLY g ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {int64(6)}})
		s.exec("ALTER TABLE p ALTER a SET DEFAULT 7", 0)
		s.exec("ALTER TABLE ONLY p ALTER a SET DEFAULT 8", 0)
		s.exec("INSERT INTO p DEFAULT VALUES", 1)
		s.exec("INSERT INTO g DEFAULT VALUES", 1)
		s.values("SELECT a FROM ONLY p", []string{"a"}, []string{"INT4"}, [][]any{{int64(8)}})
		s.values("SELECT a FROM ONLY g ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {int64(6)}, {int64(7)}})
	})
}
