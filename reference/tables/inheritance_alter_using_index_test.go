package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceAlterUsingIndexPrimary(t *testing.T) {
	run := func(name string, body func(*inheritanceCase)) {
		t.Run(name, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				body(s)
			})
		})
	}
	for _, only := range []bool{false, true} {
		name := "fresh_default"
		if only {
			name = "fresh_only"
		}
		run(name, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE c() INHERITS(p)", 0)
			s.exec("INSERT INTO p VALUES(1)", 1)
			s.exec("INSERT INTO c VALUES(NULL)", 1)
			s.exec("CREATE UNIQUE INDEX physical ON p(a)", 0)
			q := "ALTER TABLE p ADD CONSTRAINT physical PRIMARY KEY USING INDEX physical"
			if only {
				q = "ALTER TABLE ONLY p ADD CONSTRAINT physical PRIMARY KEY USING INDEX physical"
			}
			if only {
				s.fail(q, fixture.Diagnostic{Code: "42P16", Message: "constraint must be added to child tables too"})
			} else {
				s.fail(q, fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "c" contains null values`, Schema: "public", Table: "c", Column: "a"})
			}
			inheritanceAlterFacts(s, "p", nil)
			inheritanceAlterFacts(s, "c", nil)
			s.values("SELECT count(*) AS n FROM pg_index WHERE indrelid='p'::regclass::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
			s.exec("UPDATE c SET a=1", 1)
			if !only {
				s.exec(q, 0)
				inheritanceAlterFacts(s, "p", [][]any{{"p_a_not_null", true, int64(0), false}, {"physical", true, int64(0), true}})
				inheritanceAlterFacts(s, "c", [][]any{{"p_a_not_null", false, int64(1), false}})
				s.values("SELECT count(*) AS n FROM pg_index WHERE indrelid='c'::regclass::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
				s.exec("INSERT INTO c VALUES(1)", 1)
				s.fail("INSERT INTO c VALUES(NULL)", inheritanceNullDiagnostic("c", "a", "null"))
				s.run(fixture.ErrorDiagnostic("INSERT INTO p VALUES(1)", fixture.UniqueReferenceDuplicate("p", "physical", "Key (a)=(1) already exists.")))
			}
		})
	}
	for _, only := range []bool{false, true} {
		name := "existing_root_stops"
		if only {
			name = "existing_root_only"
		}
		run(name, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL)", 0)
			s.exec("CREATE TABLE c() INHERITS(p)", 0)
			s.exec("INSERT INTO p VALUES(1)", 1)
			s.exec("INSERT INTO c VALUES(1)", 1)
			s.exec("CREATE UNIQUE INDEX physical ON p(a)", 0)
			parentNN := inheritanceAlterConstraintOID(s, "p", "nn")
			childNN := inheritanceAlterConstraintOID(s, "c", "nn")
			inheritanceQueryRole(s, "using_parent_owner", "NOBYPASSRLS")
			inheritanceQueryRole(s, "using_child_owner", "NOBYPASSRLS")
			s.exec("ALTER TABLE p OWNER TO using_parent_owner", 0)
			s.exec("ALTER TABLE c OWNER TO using_child_owner", 0)
			s.exec("SET ROLE using_parent_owner", 0)
			target := "p"
			if only {
				target = "ONLY p"
			}
			s.exec("ALTER TABLE "+target+" ADD CONSTRAINT physical PRIMARY KEY USING INDEX physical", 0)
			if inheritanceAlterConstraintOID(s, "p", "nn") != parentNN || inheritanceAlterConstraintOID(s, "c", "nn") != childNN {
				s.t.Fatal("existing root NN was rebuilt or propagated again")
			}
			inheritanceAlterFacts(s, "p", [][]any{{"nn", true, int64(0), false}, {"physical", true, int64(0), true}})
			inheritanceAlterFacts(s, "c", [][]any{{"nn", false, int64(1), false}})
			s.exec("SET ROLE NONE", 0)
		})
	}
	run("nonprefix_key_not_include", func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(prefix TEXT,a INT,b INT)", 0)
		s.exec("CREATE TABLE q(z BOOL,prefix TEXT)", 0)
		s.exec("CREATE TABLE c() INHERITS(q,p)", 0)
		s.exec("INSERT INTO p VALUES('p',1,NULL)", 1)
		s.exec("INSERT INTO c VALUES(true,'c',2,NULL)", 1)
		s.exec("CREATE UNIQUE INDEX physical ON p(a) INCLUDE(b)", 0)
		s.exec("ALTER TABLE p ADD CONSTRAINT physical PRIMARY KEY USING INDEX physical", 0)
		inheritanceAlterFacts(s, "c", [][]any{{"p_a_not_null", false, int64(1), false}})
		s.values("SELECT attname,attnotnull,attislocal,attinhcount FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attnum>0 ORDER BY attname", []string{"attname", "attnotnull", "attislocal", "attinhcount"}, []string{"NAME", "BOOL", "BOOL", "INT2"}, [][]any{{"a", true, false, int64(1)}, {"b", false, false, int64(1)}, {"prefix", false, false, int64(2)}, {"z", false, false, int64(1)}})
		s.values("SELECT z,prefix,a,b FROM ONLY c", []string{"z", "prefix", "a", "b"}, []string{"BOOL", "TEXT", "INT4", "INT4"}, [][]any{{true, "c", int64(2), nil}})
		s.exec("INSERT INTO c VALUES(false,'again',2,NULL)", 1)
		s.fail("INSERT INTO c VALUES(false,'bad',NULL,NULL)", inheritanceNullDiagnostic("c", "a", "f, bad, null, null"))
		s.values("SELECT count(*) AS n FROM pg_index WHERE indrelid='c'::regclass::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}
