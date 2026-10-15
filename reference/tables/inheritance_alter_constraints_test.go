package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceAlterNotNull(t *testing.T) {
	for _, add := range []bool{false, true} {
		t.Run(map[bool]string{false: "set", true: "add"}[add], func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE p(a INT)", 0)
				s.exec("CREATE TABLE c() INHERITS(p)", 0)
				s.exec("INSERT INTO p VALUES(1)", 1)
				s.exec("INSERT INTO c VALUES(NULL)", 1)
				action := "ALTER a SET NOT NULL"
				name := "p_a_not_null"
				if add {
					action = "ADD CONSTRAINT nn NOT NULL a"
					name = "nn"
				}
				s.fail("ALTER TABLE p "+action, fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "c" contains null values`, Schema: "public", Table: "c", Column: "a"})
				inheritanceAlterFacts(s, "p", nil)
				inheritanceAlterFacts(s, "c", nil)
				if add {
					s.fail("ALTER TABLE ONLY p "+action, fixture.Diagnostic{Code: "42P16", Message: "constraint must be added to child tables too"})
					q := "ALTER TABLE ONLY p ADD CONSTRAINT physical NOT NULL a NO INHERIT"
					if s.b.PG {
						s.exec(q, 0)
						inheritanceAlterFacts(s, "p", [][]any{{"physical", true, int64(0), true}})
						s.exec("ALTER TABLE p DROP CONSTRAINT physical", 0)
					} else {
						step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "NOT NULL modifiers are unsupported", Position: strconv.Itoa(strings.Index(q, "NO INHERIT") + 1)})
						step.ErrorStage = "execute"
						if s.prepared {
							step.ErrorStage = "prepare"
						}
						s.run(step)
						inheritanceAlterFacts(s, "p", nil)
					}
				} else {
					s.exec("ALTER TABLE ONLY p "+action, 0)
					inheritanceAlterFacts(s, "p", [][]any{{name, true, int64(0), true}})
					inheritanceAlterFacts(s, "c", nil)
					s.exec("INSERT INTO c VALUES(NULL)", 1)
					s.exec("ALTER TABLE ONLY p ALTER a DROP NOT NULL", 0)
				}
				s.exec("UPDATE c SET a=2 WHERE a IS NULL", map[bool]int64{false: 2, true: 1}[add])
				s.exec("ALTER TABLE p "+action, 0)
				inheritanceAlterFacts(s, "p", [][]any{{name, true, int64(0), false}})
				inheritanceAlterFacts(s, "c", [][]any{{name, false, int64(1), false}})
				// Repeated SET is a no-op, not another inherited contribution.
				s.exec("ALTER TABLE p ALTER a SET NOT NULL", 0)
				inheritanceAlterFacts(s, "c", [][]any{{name, false, int64(1), false}})
				s.fail("ALTER TABLE c ALTER a DROP NOT NULL", fixture.Diagnostic{Code: "42P16", Message: `cannot drop inherited constraint "` + name + `" of relation "c"`})
				s.fail("UPDATE p SET a=NULL", inheritanceNullDiagnostic("p", "a", "null"))
				s.exec("ALTER TABLE p ALTER a DROP NOT NULL", 0)
				inheritanceAlterFacts(s, "p", nil)
				inheritanceAlterFacts(s, "c", nil)
				s.exec("INSERT INTO c VALUES(NULL)", 1)
			})
		})
	}
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("INSERT INTO c VALUES(NULL)", 1)
		s.exec("ALTER TABLE p ADD CONSTRAINT nn NOT NULL a NOT VALID", 0)
		s.values("SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='c'::regclass::oid AND conname='nn'", []string{"definition"}, []string{"TEXT"}, [][]any{{"NOT NULL a NOT VALID"}})
		s.fail("INSERT INTO c VALUES(NULL)", inheritanceNullDiagnostic("c", "a", "null"))
		s.values("SELECT a FROM c", []string{"a"}, []string{"INT4"}, [][]any{{nil}})
	})
}

func TestReferenceInheritanceAlterChecks(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(-2)", 1)
		s.fail("ALTER TABLE p ADD CONSTRAINT ck CHECK(a>0)", fixture.Diagnostic{Code: "23514", Message: `check constraint "ck" of relation "c" is violated by some row`, Schema: "public", Table: "c", Constraint: "ck"})
		inheritanceAlterFacts(s, "p", nil)
		inheritanceAlterFacts(s, "c", nil)
		s.fail("ALTER TABLE ONLY p ADD CONSTRAINT ck CHECK(a>0)", fixture.Diagnostic{Code: "42P16", Message: "constraint must be added to child tables too"})
		s.exec("ALTER TABLE p ADD CONSTRAINT physical CHECK(a>0) NO INHERIT", 0)
		inheritanceAlterFacts(s, "p", [][]any{{"physical", true, int64(0), true}})
		inheritanceAlterFacts(s, "c", nil)
		s.exec("UPDATE c SET a=2", 1)
		s.exec("ALTER TABLE p ADD CONSTRAINT ck CHECK(a>0)", 0)
		inheritanceAlterFacts(s, "c", [][]any{{"ck", false, int64(1), false}})
		s.fail("UPDATE p SET a=-1 WHERE a=2", inheritanceCheckDiagnostic("c", "ck", "-1"))
		s.exec("ALTER TABLE p ADD CONSTRAINT future CHECK(a<2) NOT VALID", 0)
		s.values("SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='c'::regclass::oid AND conname='future'", []string{"definition"}, []string{"TEXT"}, [][]any{{"CHECK ((a < 2)) NOT VALID"}})
		s.fail("INSERT INTO c VALUES(3)", inheritanceCheckDiagnostic("c", "future", "3"))
		q := "ALTER TABLE p ADD CONSTRAINT passive CHECK(a<0) NOT ENFORCED"
		if s.b.PG {
			s.exec(q, 0)
		} else {
			step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "constraint modifiers are unsupported", Position: strconv.Itoa(strings.Index(q, "NOT ENFORCED") + 1)})
			step.ErrorStage = "execute"
			if s.prepared {
				step.ErrorStage = "prepare"
			}
			s.run(step)
		}
		s.exec("INSERT INTO c VALUES(1)", 1)
		s.values("SELECT a FROM c ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
	})
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c(a INT,CONSTRAINT ck CHECK(a>0)) INHERITS(p)", 0)
		s.exec("ALTER TABLE p ADD CONSTRAINT ck CHECK(a>0)", 0)
		inheritanceAlterFacts(s, "c", [][]any{{"ck", true, int64(1), false}})
		s.exec("ALTER TABLE p DROP CONSTRAINT ck", 0)
		inheritanceAlterFacts(s, "c", [][]any{{"ck", true, int64(0), false}})
	})
}

func TestReferenceInheritanceAlterConstraintOrigins(t *testing.T) {
	for _, kind := range []string{"check", "notnull"} {
		for _, only := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "_recursive", true: "_only"}[only], func(t *testing.T) {
				inheritanceScenario(t, func(s *inheritanceCase) {
					decl := "a INT,CONSTRAINT fact CHECK(a>0)"
					if kind == "notnull" {
						decl = "a INT CONSTRAINT fact NOT NULL"
					}
					s.exec("CREATE TABLE p("+decl+")", 0)
					s.exec("CREATE TABLE other("+decl+")", 0)
					s.exec("CREATE TABLE c() INHERITS(p)", 0)
					s.exec("CREATE TABLE g() INHERITS(c)", 0)
					s.exec("CREATE TABLE shared() INHERITS(p,other)", 0)
					s.exec("CREATE TABLE local("+decl+") INHERITS(p)", 0)
					inheritanceAlterFacts(s, "shared", [][]any{{"fact", false, int64(2), false}})
					s.fail("ALTER TABLE c DROP CONSTRAINT fact", fixture.Diagnostic{Code: "42P16", Message: `cannot drop inherited constraint "fact" of relation "c"`})
					target := "p"
					if only {
						target = "ONLY p"
					}
					s.exec("ALTER TABLE "+target+" DROP CONSTRAINT fact", 0)
					inheritanceAlterFacts(s, "p", nil)
					if only {
						inheritanceAlterFacts(s, "c", [][]any{{"fact", true, int64(0), false}})
						inheritanceAlterFacts(s, "g", [][]any{{"fact", false, int64(1), false}})
					} else {
						inheritanceAlterFacts(s, "c", nil)
						inheritanceAlterFacts(s, "g", nil)
					}
					inheritanceAlterFacts(s, "shared", [][]any{{"fact", false, int64(1), false}})
					inheritanceAlterFacts(s, "local", [][]any{{"fact", true, int64(0), false}})
					invalid := "NULL"
					if kind == "check" {
						invalid = "-1"
					}
					s.exec("INSERT INTO p VALUES("+invalid+")", 1)
					if !only {
						s.exec("INSERT INTO c VALUES("+invalid+")", 1)
					}
					s.exec("INSERT INTO shared VALUES(5)", 1)
				})
			})
		}
	}
}
