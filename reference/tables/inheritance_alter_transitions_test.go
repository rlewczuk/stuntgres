package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceAlterConstraintTransitions(t *testing.T) {
	run := func(name string, body func(*inheritanceCase)) {
		t.Run(name, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				body(s)
			})
		})
	}
	for _, shared := range []bool{false, true} {
		name := "drop_column_local"
		if shared {
			name = "drop_column_shared"
		}
		run(name, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL,CONSTRAINT ck CHECK(a>0))", 0)
			child := "CREATE TABLE c(a INT) INHERITS(p)"
			if shared {
				s.exec("CREATE TABLE q(a INT CONSTRAINT nn NOT NULL,CONSTRAINT ck CHECK(a>0))", 0)
				child = "CREATE TABLE c() INHERITS(p,q)"
			}
			s.exec(child, 0)
			s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
			s.exec("INSERT INTO p VALUES(1)", 1)
			s.exec("INSERT INTO c VALUES(2)", 1)
			s.exec("INSERT INTO leaf VALUES(3)", 1)
			nn := inheritanceAlterConstraintOID(s, "c", "nn")
			ck := inheritanceAlterConstraintOID(s, "c", "ck")
			s.exec("ALTER TABLE p DROP COLUMN a", 0)
			count := int64(1)
			attributeCount := int64(0)
			if shared {
				count = 2
				attributeCount = 1
			}
			checks := []fixture.Step{
				fixture.Select("SELECT attname,attnum,attislocal,attinhcount FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum", "attislocal", "attinhcount"}, []string{"NAME", "INT2", "BOOL", "INT2"}, [][]any{{"a", int64(1), !shared, attributeCount}}),
				fixture.Select("SELECT conname,conislocal,coninhcount,connoinherit FROM pg_constraint WHERE conrelid='c'::regclass::oid ORDER BY conname", []string{"conname", "conislocal", "coninhcount", "connoinherit"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, [][]any{{"ck", false, count, false}, {"nn", false, count, false}}),
				fixture.Select("SELECT conname,conislocal,coninhcount,connoinherit FROM pg_constraint WHERE conrelid='leaf'::regclass::oid ORDER BY conname", []string{"conname", "conislocal", "coninhcount", "connoinherit"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, [][]any{{"ck", false, int64(1), false}, {"nn", false, int64(1), false}}),
				fixture.Select("SELECT a FROM c ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}),
				fixture.Select("SELECT count(*) AS n FROM p", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}),
			}
			inheritanceAlterFacts(s, "p", nil)
			if inheritanceAlterConstraintOID(s, "c", "nn") != nn || inheritanceAlterConstraintOID(s, "c", "ck") != ck {
				s.t.Fatal("retained column reminted physical constraints")
			}
			inheritanceMutationRoundTrip(s, checks)
			s.fail("INSERT INTO c VALUES(NULL)", inheritanceNullDiagnostic("c", "a", "null"))
			s.fail("INSERT INTO c VALUES(0)", inheritanceCheckDiagnostic("c", "ck", "0"))
		})
	}
	run("drop_set_notnull", func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL)", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(2)", 1)
		s.exec("INSERT INTO leaf VALUES(3)", 1)
		previous := map[string]int64{}
		for _, table := range []string{"p", "c", "leaf"} {
			previous[table] = inheritanceAlterConstraintOID(s, table, "nn")
		}
		s.exec("ALTER TABLE p DROP CONSTRAINT nn,ALTER a SET NOT NULL", 0)
		for _, table := range []string{"p", "c", "leaf"} {
			count := int64(1)
			if table == "p" {
				count = 0
			}
			inheritanceAlterFacts(s, table, [][]any{{"p_a_not_null", table == "p", count, false}})
			if inheritanceAlterConstraintOID(s, table, "p_a_not_null") == previous[table] {
				s.t.Fatal("DROP/SET retained retired NN identity")
			}
		}
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
	})
	run("drop_add_check", func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
		s.exec("INSERT INTO p VALUES(2)", 1)
		s.exec("INSERT INTO c VALUES(3)", 1)
		s.exec("INSERT INTO leaf VALUES(4)", 1)
		previous := map[string]int64{}
		for _, table := range []string{"p", "c", "leaf"} {
			previous[table] = inheritanceAlterConstraintOID(s, table, "ck")
		}
		s.fail("ALTER TABLE p DROP CONSTRAINT ck,ADD CONSTRAINT ck CHECK(a<4)", fixture.Diagnostic{Code: "23514", Message: `check constraint "ck" of relation "leaf" is violated by some row`, Schema: "public", Table: "leaf", Constraint: "ck"})
		for _, table := range []string{"p", "c", "leaf"} {
			if inheritanceAlterConstraintOID(s, table, "ck") != previous[table] {
				s.t.Fatal("failed DROP/ADD changed physical CHECK identity")
			}
		}
		s.exec("ALTER TABLE p DROP CONSTRAINT ck,ADD CONSTRAINT ck CHECK(a>1)", 0)
		for _, table := range []string{"p", "c", "leaf"} {
			count := int64(1)
			if table == "p" {
				count = 0
			}
			inheritanceAlterFacts(s, table, [][]any{{"ck", table == "p", count, false}})
			if inheritanceAlterConstraintOID(s, table, "ck") == previous[table] {
				s.t.Fatal("DROP/ADD retained retired CHECK identity")
			}
			s.values("SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='"+table+"'::regclass::oid", []string{"definition"}, []string{"TEXT"}, [][]any{{"CHECK ((a > 1))"}})
		}
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(4)}})
	})
	for _, only := range []bool{false, true} {
		name := "validate_recursive_atomic"
		if only {
			name = "validate_only"
		}
		run(name, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE c() INHERITS(p)", 0)
			s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
			s.exec("INSERT INTO c VALUES(NULL)", 1)
			s.exec("INSERT INTO leaf VALUES(NULL)", 1)
			s.exec("ALTER TABLE p ADD CONSTRAINT nn NOT NULL a NOT VALID", 0)
			s.exec("ALTER TABLE c ALTER a SET NOT NULL", 0)
			previous := inheritanceAlterConstraintOID(s, "c", "nn")
			leaf := inheritanceAlterConstraintOID(s, "leaf", "nn")
			s.exec("UPDATE ONLY c SET a=7", 1)
			if only {
				s.fail("ALTER TABLE ONLY c ALTER a SET NOT NULL", fixture.Diagnostic{Code: "42P16", Message: "constraint must be validated on child tables too"})
			} else {
				s.fail("ALTER TABLE c ALTER a SET NOT NULL", fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "leaf" contains null values`, Schema: "public", Table: "leaf", Column: "a"})
				s.values("SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='c'::regclass::oid", []string{"definition"}, []string{"TEXT"}, [][]any{{"NOT NULL a NOT VALID"}})
				s.exec("UPDATE ONLY leaf SET a=7", 1)
				s.exec("ALTER TABLE c ALTER a SET NOT NULL", 0)
			}
			inheritanceAlterFacts(s, "c", [][]any{{"nn", true, int64(1), false}})
			inheritanceAlterFacts(s, "leaf", [][]any{{"nn", false, int64(1), false}})
			definition := "NOT NULL a"
			if only {
				definition += " NOT VALID"
			}
			s.values("SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='c'::regclass::oid", []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
			s.values("SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='leaf'::regclass::oid", []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
			if inheritanceAlterConstraintOID(s, "c", "nn") != previous || inheritanceAlterConstraintOID(s, "leaf", "nn") != leaf {
				s.t.Fatal("validation changed physical NN identity")
			}
			if only {
				// A childless existing local fact can genuinely complete through ONLY;
				// rejecting the graph case above must not erase this supported transition.
				s.exec("CREATE TABLE standalone(a INT)", 0)
				s.exec("INSERT INTO standalone VALUES(NULL)", 1)
				s.exec("ALTER TABLE standalone ADD CONSTRAINT standalone_nn NOT NULL a NOT VALID", 0)
				previous := inheritanceAlterConstraintOID(s, "standalone", "standalone_nn")
				s.exec("UPDATE standalone SET a=7", 1)
				s.exec("ALTER TABLE ONLY standalone ALTER a SET NOT NULL", 0)
				inheritanceAlterFacts(s, "standalone", [][]any{{"standalone_nn", true, int64(0), false}})
				s.values("SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='standalone'::regclass::oid", []string{"definition"}, []string{"TEXT"}, [][]any{{"NOT NULL a"}})
				s.values("SELECT a FROM standalone", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
				if inheritanceAlterConstraintOID(s, "standalone", "standalone_nn") != previous {
					s.t.Fatal("ONLY validation reminted childless NN identity")
				}
			}
		})
	}
	run("set_notnull_noinherit_child", func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
		s.exec("ALTER TABLE ONLY c ALTER a SET NOT NULL", 0)
		previous := inheritanceAlterConstraintOID(s, "c", "c_a_not_null")
		s.fail("ALTER TABLE p ALTER a SET NOT NULL", fixture.Diagnostic{Code: "0A000", Message: `cannot change NO INHERIT status of NOT NULL constraint "c_a_not_null" on relation "c"`})
		inheritanceAlterFacts(s, "p", nil)
		inheritanceAlterFacts(s, "c", [][]any{{"c_a_not_null", true, int64(0), true}})
		inheritanceAlterFacts(s, "leaf", nil)
		if inheritanceAlterConstraintOID(s, "c", "c_a_not_null") != previous {
			s.t.Fatal("failed SET changed child NN identity")
		}
		s.exec("INSERT INTO p VALUES(NULL)", 1)
		s.exec("INSERT INTO leaf VALUES(NULL)", 1)
	})
}
