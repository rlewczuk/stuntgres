package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceAlterMergeStops(t *testing.T) {
	run := func(name string, body func(*inheritanceCase)) {
		t.Run(name, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				body(s)
			})
		})
	}
	for _, kind := range []string{"nn", "check"} {
		run("fresh_"+kind+"_mixed_owner", func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE c() INHERITS(p)", 0)
			s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
			s.exec("INSERT INTO p VALUES(1)", 1)
			s.exec("INSERT INTO c VALUES(2)", 1)
			s.exec("INSERT INTO leaf VALUES(3)", 1)
			role := "inherit_fresh_" + kind + "_owner"
			inheritanceQueryRole(s, role, "NOBYPASSRLS")
			s.exec("ALTER TABLE p OWNER TO "+role, 0)
			s.exec("SET ROLE "+role, 0)
			query := "ALTER TABLE p ALTER a SET NOT NULL"
			if kind == "check" {
				query = "ALTER TABLE p ADD CONSTRAINT ck CHECK(a>0)"
			}
			s.fail(query, fixture.Diagnostic{Code: "42501", Message: "must be owner of table c"})
			inheritanceAlterFacts(s, "p", nil)
			inheritanceAlterFacts(s, "c", nil)
			inheritanceAlterFacts(s, "leaf", nil)
			s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
			s.exec("SET ROLE NONE", 0)
		})
	}
	run("check_only_localization", func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
		inheritanceQueryRole(s, "inherit_merge_only_check_owner", "NOBYPASSRLS")
		s.exec("ALTER TABLE c OWNER TO inherit_merge_only_check_owner", 0)
		s.exec("SET ROLE inherit_merge_only_check_owner", 0)
		s.exec("ALTER TABLE ONLY c ADD CONSTRAINT ck CHECK(a>0)", 0)
		inheritanceAlterFacts(s, "c", [][]any{{"ck", true, int64(1), false}})
		inheritanceAlterFacts(s, "leaf", [][]any{{"ck", false, int64(1), false}})
		s.exec("SET ROLE NONE", 0)
	})
	for _, existing := range []bool{false, true} {
		name := "nn_direct_localization"
		if existing {
			name = "nn_descendant_merge"
		}
		run(name, func(s *inheritanceCase) {
			parent := "a INT CONSTRAINT nn NOT NULL"
			child := ""
			if existing {
				parent = "a INT"
				child = "a INT CONSTRAINT nn NOT NULL"
			}
			s.exec("CREATE TABLE p("+parent+")", 0)
			s.exec("CREATE TABLE c("+child+") INHERITS(p)", 0)
			s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
			previous := inheritanceAlterConstraintOID(s, "c", "nn")
			inheritanceQueryRole(s, "inherit_merge_nn_owner", "NOBYPASSRLS")
			s.exec("ALTER TABLE p OWNER TO inherit_merge_nn_owner", 0)
			s.exec("ALTER TABLE c OWNER TO inherit_merge_nn_owner", 0)
			s.exec("SET ROLE inherit_merge_nn_owner", 0)
			// This role owns p/c but not leaf. Existing facts must stop before leaf's
			// owner/AX admission rather than merely avoid a duplicate rewrite.
			s.exec("ALTER TABLE p ALTER a SET NOT NULL", 0)
			if !existing {
				s.exec("ALTER TABLE c ALTER a SET NOT NULL", 0)
			}
			inheritanceAlterFacts(s, "c", [][]any{{"nn", true, int64(1), false}})
			inheritanceAlterFacts(s, "leaf", [][]any{{"nn", false, int64(1), false}})
			if inheritanceAlterConstraintOID(s, "c", "nn") != previous {
				s.t.Fatal("merge reminted physical NN identity")
			}
			s.exec("ALTER TABLE p ALTER a DROP NOT NULL", 0)
			inheritanceAlterFacts(s, "p", nil)
			inheritanceAlterFacts(s, "c", [][]any{{"nn", true, int64(0), false}})
			inheritanceAlterFacts(s, "leaf", [][]any{{"nn", false, int64(1), false}})
			s.exec("INSERT INTO p VALUES(NULL)", 1)
			s.fail("INSERT INTO c VALUES(NULL)", inheritanceNullDiagnostic("c", "a", "null"))
			s.exec("SET ROLE NONE", 0)
		})
	}
	for _, existing := range []bool{false, true} {
		name := "check_direct_localization"
		if existing {
			name = "check_descendant_merge"
		}
		run(name, func(s *inheritanceCase) {
			// CURRENT_USER makes any accidental historical rescan observable after
			// switching to the non-superuser owner. Genuine compatible merging scans no
			// historical rows; subsequent INSERT still enforces the actual CHECK.
			var bootstrapOwner string
			if err := s.c.QueryRowContext(s.p.Ctx, "SELECT current_user").Scan(&bootstrapOwner); err != nil {
				s.t.Fatal(err)
			}
			predicate := "current_user='" + strings.ReplaceAll(bootstrapOwner, "'", "''") + "'"
			parent := "a INT,CONSTRAINT ck CHECK(" + predicate + ")"
			child := ""
			if existing {
				parent = "a INT"
				child = "CONSTRAINT ck CHECK(" + predicate + ")"
			}
			s.exec("CREATE TABLE p("+parent+")", 0)
			s.exec("CREATE TABLE c("+child+") INHERITS(p)", 0)
			s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
			s.exec("INSERT INTO c VALUES(1)", 1)
			previous := inheritanceAlterConstraintOID(s, "c", "ck")
			inheritanceQueryRole(s, "inherit_merge_check_owner", "NOBYPASSRLS")
			s.exec("ALTER TABLE p OWNER TO inherit_merge_check_owner", 0)
			s.exec("ALTER TABLE c OWNER TO inherit_merge_check_owner", 0)
			s.exec("SET ROLE inherit_merge_check_owner", 0)
			s.values("SELECT "+predicate+" AS original_owner", []string{"original_owner"}, []string{"BOOL"}, [][]any{{false}})
			target := "c"
			if existing {
				target = "p"
			}
			s.exec("ALTER TABLE "+target+" ADD CONSTRAINT ck CHECK("+predicate+")", 0)
			inheritanceAlterFacts(s, "c", [][]any{{"ck", true, int64(1), false}})
			inheritanceAlterFacts(s, "leaf", [][]any{{"ck", false, int64(1), false}})
			if inheritanceAlterConstraintOID(s, "c", "ck") != previous {
				s.t.Fatal("merge reminted physical CHECK identity")
			}
			s.values("SELECT a FROM ONLY c", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
			s.fail("ALTER TABLE c ADD CONSTRAINT ck CHECK("+predicate+")", fixture.Diagnostic{Code: "42710", Message: `constraint "ck" for relation "c" already exists`})
			s.exec("ALTER TABLE p DROP CONSTRAINT ck", 0)
			inheritanceAlterFacts(s, "p", nil)
			inheritanceAlterFacts(s, "c", [][]any{{"ck", true, int64(0), false}})
			inheritanceAlterFacts(s, "leaf", [][]any{{"ck", false, int64(1), false}})
			s.exec("INSERT INTO p VALUES(-1)", 1)
			s.fail("INSERT INTO c VALUES(-1)", inheritanceCheckDiagnostic("c", "ck", "-1"))
			s.exec("SET ROLE NONE", 0)
		})
	}
	run("check_invalid_merge", func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(-1)", 1)
		s.exec("ALTER TABLE c ADD CONSTRAINT ck CHECK(a>0) NOT VALID", 0)
		previous := inheritanceAlterConstraintOID(s, "c", "ck")
		s.fail("ALTER TABLE p ADD CONSTRAINT ck CHECK(a>0)", fixture.Diagnostic{Code: "42P17", Message: `constraint "ck" conflicts with NOT VALID constraint on relation "c"`})
		inheritanceAlterFacts(s, "p", nil)
		inheritanceAlterFacts(s, "c", [][]any{{"ck", true, int64(0), false}})
		s.values("SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='c'::regclass::oid", []string{"definition"}, []string{"TEXT"}, [][]any{{"CHECK ((a > 0)) NOT VALID"}})
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(1)}})
		if inheritanceAlterConstraintOID(s, "c", "ck") != previous {
			s.t.Fatal("failed merge changed CHECK identity")
		}
	})
	run("nn_invalid_localization", func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
		s.exec("INSERT INTO c VALUES(NULL)", 1)
		s.exec("ALTER TABLE p ADD CONSTRAINT nn NOT NULL a NOT VALID", 0)
		s.exec("ALTER TABLE c ALTER a SET NOT NULL", 0)
		inheritanceAlterFacts(s, "c", [][]any{{"nn", true, int64(1), false}})
		s.values("SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='c'::regclass::oid", []string{"definition"}, []string{"TEXT"}, [][]any{{"NOT NULL a NOT VALID"}})
		s.values("SELECT a FROM ONLY c", []string{"a"}, []string{"INT4"}, [][]any{{nil}})
		s.fail("ALTER TABLE c ALTER a SET NOT NULL", fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "c" contains null values`, Schema: "public", Table: "c", Column: "a"})
		s.values("SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='c'::regclass::oid", []string{"definition"}, []string{"TEXT"}, [][]any{{"NOT NULL a NOT VALID"}})
	})
	run("nn_noinherit_repeat", func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("ALTER TABLE ONLY p ALTER a SET NOT NULL", 0)
		diagnostic := fixture.Diagnostic{Code: "0A000", Message: `cannot change NO INHERIT status of NOT NULL constraint "p_a_not_null" on relation "p"`}
		s.fail("ALTER TABLE p ALTER a SET NOT NULL", diagnostic)
		s.exec("ALTER TABLE ONLY p ALTER a SET NOT NULL", 0)
		inheritanceAlterFacts(s, "p", [][]any{{"p_a_not_null", true, int64(0), true}})
		inheritanceAlterFacts(s, "c", nil)
		s.exec("INSERT INTO c VALUES(NULL)", 1)
		s.exec("DROP TABLE c", 0)
		s.fail("ALTER TABLE p ALTER a SET NOT NULL", diagnostic)
		inheritanceAlterFacts(s, "p", [][]any{{"p_a_not_null", true, int64(0), true}})
	})
	run("column_existing_stop", func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c(extra INT DEFAULT 9) INHERITS(p)", 0)
		s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
		s.exec("INSERT INTO c(a,extra) VALUES(1,19)", 1)
		s.exec("INSERT INTO leaf(a,extra) VALUES(2,29)", 1)
		inheritanceQueryRole(s, "inherit_merge_column_owner", "NOBYPASSRLS")
		s.exec("ALTER TABLE p OWNER TO inherit_merge_column_owner", 0)
		s.exec("ALTER TABLE c OWNER TO inherit_merge_column_owner", 0)
		s.exec("SET ROLE inherit_merge_column_owner", 0)
		s.exec("ALTER TABLE p ADD COLUMN extra INT DEFAULT 7", 0)
		s.exec("INSERT INTO p(a) VALUES(3)", 1)
		s.exec("INSERT INTO c(a) VALUES(4)", 1)
		s.values("SELECT a,extra FROM p ORDER BY a", []string{"a", "extra"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(19)}, {int64(2), int64(29)}, {int64(3), int64(7)}, {int64(4), int64(9)}})
		inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), false, int64(1)}, {"extra", int64(2), true, int64(1)}})
		inheritanceAlterAttributes(s, "leaf", [][]any{{"a", int64(1), false, int64(1)}, {"extra", int64(2), false, int64(1)}})
		s.exec("SET ROLE NONE", 0)
	})
	for _, kind := range []string{"notnull", "check"} {
		run("column_existing_"+kind, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE c(extra INT DEFAULT 9) INHERITS(p)", 0)
			s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
			s.exec("INSERT INTO c(a,extra) VALUES(1,19)", 1)
			bad := "NULL"
			suffix := "NOT NULL"
			name := "p_extra_not_null"
			diagnostic := fixture.Diagnostic{Code: "23502", Message: `column "extra" of relation "leaf" contains null values`, Schema: "public", Table: "leaf", Column: "extra"}
			if kind == "check" {
				bad = "-1"
				suffix = "CHECK(extra>0)"
				name = "p_extra_check"
				diagnostic = fixture.Diagnostic{Code: "23514", Message: `check constraint "p_extra_check" of relation "leaf" is violated by some row`, Schema: "public", Table: "leaf", Constraint: "p_extra_check"}
			}
			s.exec("INSERT INTO leaf(a,extra) VALUES(2,"+bad+")", 1)
			query := "ALTER TABLE p ADD COLUMN extra INT DEFAULT 7 " + suffix
			s.fail(query, diagnostic)
			inheritanceAlterAttributes(s, "p", [][]any{{"a", int64(1), true, int64(0)}})
			inheritanceAlterFacts(s, "p", nil)
			inheritanceAlterFacts(s, "c", nil)
			inheritanceAlterFacts(s, "leaf", nil)
			s.exec("UPDATE leaf SET extra=2", 1)
			s.exec(query, 0)
			inheritanceAlterFacts(s, "p", [][]any{{name, true, int64(0), false}})
			inheritanceAlterFacts(s, "c", [][]any{{name, false, int64(1), false}})
			inheritanceAlterFacts(s, "leaf", [][]any{{name, false, int64(1), false}})
			s.values("SELECT a,extra FROM p ORDER BY a", []string{"a", "extra"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(19)}, {int64(2), int64(2)}})
		})
	}
}
