package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceAlterFeatureSmoke(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE q(prefix BOOL)", 0)
		s.exec("CREATE TABLE p(a INT,x TEXT)", 0)
		s.exec("CREATE TABLE c(extra TEXT) INHERITS(q,p)", 0)
		s.exec("INSERT INTO p VALUES(1,'parent')", 1)
		s.exec("INSERT INTO c VALUES(true,2,'child','keep')", 1)
		s.exec("ALTER TABLE p ADD COLUMN flag INT DEFAULT 7", 0)
		s.exec("ALTER TABLE c ALTER flag SET DEFAULT 9", 0)
		s.exec("ALTER TABLE p ALTER flag SET DEFAULT 7", 0)
		s.exec("ALTER TABLE ONLY p ALTER flag SET DEFAULT 8", 0)
		s.exec("INSERT INTO p(a,x) VALUES(3,'new_parent')", 1)
		s.exec("INSERT INTO c(prefix,a,x,extra) VALUES(false,4,'new_child','new_keep')", 1)
		s.exec("ALTER TABLE p ALTER flag DROP DEFAULT", 0)
		s.exec("INSERT INTO p(a,x) VALUES(5,'null_parent')", 1)
		s.exec("INSERT INTO c(prefix,a,x,extra) VALUES(false,6,'null_child','null_keep')", 1)
		s.values("SELECT a,flag FROM p ORDER BY a", []string{"a", "flag"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(7)}, {int64(3), int64(8)}, {int64(4), int64(7)}, {int64(5), nil}, {int64(6), nil}})
		s.exec("DELETE FROM p WHERE a>2", 4)
		s.exec("ALTER TABLE p ALTER a TYPE BIGINT USING a+10", 0)
		s.exec("ALTER TABLE p RENAME flag TO mark", 0)
		inheritanceAlterAttributes(s, "c", [][]any{{"prefix", int64(1), false, int64(1)}, {"a", int64(2), false, int64(1)}, {"x", int64(3), false, int64(1)}, {"extra", int64(4), true, int64(0)}, {"mark", int64(5), false, int64(1)}})
		s.exec("ALTER TABLE p ADD CONSTRAINT small CHECK(a<20)", 0)
		s.fail("UPDATE p SET a=a+20", inheritanceCheckDiagnostic("p", "small", "31, parent, 7"))
		for _, tc := range []struct{ q, message string }{
			{"ALTER TABLE ONLY p ADD flag INT", "column must be added to child tables too"},
			{"ALTER TABLE ONLY p ALTER a TYPE INT", `type of inherited column "a" must be changed in child tables too`},
			{"ALTER TABLE ONLY p RENAME mark TO flag", `inherited column "mark" must be renamed in child tables too`},
			{"ALTER TABLE ONLY p ADD CONSTRAINT invalid CHECK(a>0)", "constraint must be added to child tables too"},
		} {
			s.fail(tc.q, fixture.Diagnostic{Code: "42P16", Message: tc.message})
		}
		s.exec("ALTER TABLE p DROP COLUMN mark", 0)
		s.exec("CREATE TABLE origin(a INT,b TEXT)", 0)
		s.exec("CREATE TABLE retained() INHERITS(origin)", 0)
		s.exec("CREATE TABLE leaf() INHERITS(retained)", 0)
		s.exec("INSERT INTO retained VALUES(7,'retained')", 1)
		s.exec("INSERT INTO leaf VALUES(8,'leaf')", 1)
		s.exec("ALTER TABLE ONLY origin DROP a", 0)
		s.exec("CREATE TABLE generated(a INT,g INT GENERATED ALWAYS AS(a+1) STORED)", 0)
		s.exec("CREATE TABLE generated_child() INHERITS(generated)", 0)
		s.exec("INSERT INTO generated(a) VALUES(1)", 1)
		s.exec("INSERT INTO generated_child(a) VALUES(2)", 1)
		s.exec("ALTER TABLE generated ALTER g SET EXPRESSION AS(a+2)", 0)
		s.values("SELECT * FROM generated ORDER BY a", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}, {int64(2), int64(4)}})
		s.exec("ALTER TABLE ONLY generated ALTER g SET EXPRESSION AS(a+3)", 0)
		s.exec("ALTER TABLE generated ALTER g DROP EXPRESSION", 0)
		s.exec("UPDATE generated_child SET a=9", 1)
		checks := []fixture.Step{
			fixture.Select("SELECT * FROM p ORDER BY a", []string{"a", "x"}, []string{"INT8", "TEXT"}, [][]any{{int64(11), "parent"}, {int64(12), "child"}}),
			fixture.Select("SELECT * FROM c", []string{"prefix", "a", "x", "extra"}, []string{"BOOL", "INT8", "TEXT", "TEXT"}, [][]any{{true, int64(12), "child", "keep"}}),
			fixture.Select("SELECT a,b FROM retained ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "retained"}, {int64(8), "leaf"}}),
			fixture.Select("SELECT attname,attislocal,attinhcount FROM pg_attribute WHERE attrelid='retained'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attname", []string{"attname", "attislocal", "attinhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"a", true, int64(0)}, {"b", false, int64(1)}}),
			fixture.Select("SELECT attname,attislocal,attinhcount FROM pg_attribute WHERE attrelid='leaf'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attname", []string{"attname", "attislocal", "attinhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"a", false, int64(1)}, {"b", false, int64(1)}}),
			fixture.Select("SELECT * FROM generated ORDER BY a", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(4)}, {int64(9), int64(4)}}),
			fixture.Select("SELECT conname,conislocal,coninhcount,connoinherit FROM pg_constraint WHERE conrelid='c'::regclass::oid ORDER BY conname", []string{"conname", "conislocal", "coninhcount", "connoinherit"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, [][]any{{"small", false, int64(1), false}}),
		}
		inheritanceMutationRoundTrip(s, checks)
	})
}
