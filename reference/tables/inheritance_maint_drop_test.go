package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInheritanceMaintDropOnlyCascade(t *testing.T) {
	t.Run("InheritedProtectionAndOnly", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT,b INT)", 0)
			s.exec("CREATE TABLE c(a INT,b INT)", 0)
			s.exec("CREATE TABLE g() INHERITS(c)", 0)
			s.exec("INSERT INTO c VALUES(2,20)", 1)
			s.exec("INSERT INTO g VALUES(3,30)", 1)
			s.exec("ALTER TABLE c INHERIT p", 0)
			s.fail("ALTER TABLE c DROP a CASCADE", fixture.Diagnostic{Code: "42P16", Message: `cannot drop inherited column "a"`})
			s.fail("ALTER TABLE c ALTER a TYPE BIGINT", fixture.Diagnostic{Code: "42P16", Message: `cannot alter inherited column "a"`, Position: "21"})
			s.fail("ALTER TABLE ONLY p ADD blocked INT", fixture.Diagnostic{Code: "42P16", Message: "column must be added to child tables too"})
			s.fail("ALTER TABLE ONLY p ADD CONSTRAINT blocked CHECK(a>0)", fixture.Diagnostic{Code: "42P16", Message: "constraint must be added to child tables too"})
			s.exec("ALTER TABLE ONLY p DROP a", 0)
			inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), true, int64(0)}, {"b", int64(2), true, int64(1)}})
			inheritanceAlterAttributes(s, "g", [][]any{{"a", int64(1), false, int64(1)}, {"b", int64(2), false, int64(1)}})
			s.values("SELECT a,b FROM c ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}})
			s.exec("CREATE VIEW retained AS SELECT b FROM p", 0)
			s.fail("ALTER TABLE p DROP b", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column b of table p because other objects depend on it", Detail: "view retained depends on column b of table p", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			s.exec("ALTER TABLE p DROP b CASCADE", 0)
			s.absent("retained")
			// The independently local b survives; its contribution disappears.
			inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), true, int64(0)}, {"b", int64(2), true, int64(0)}})
			s.values("SELECT a,b FROM c ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}})
		})
	})
	t.Run("InheritedOnlyRecursiveRemoval", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT,b INT)", 0)
			s.exec("CREATE TABLE c() INHERITS(p)", 0)
			s.exec("ALTER TABLE c NO INHERIT p,INHERIT p", 0)
			// Detach earned locality, so use a freshly inherited-only descendant.
			s.exec("CREATE TABLE fresh() INHERITS(p)", 0)
			s.exec("CREATE TABLE leaf() INHERITS(fresh)", 0)
			s.exec("INSERT INTO leaf VALUES(3,30)", 1)
			s.exec("ALTER TABLE p DROP a RESTRICT", 0)
			inheritanceAlterAttributes(s, "fresh", [][]any{{"b", int64(2), false, int64(1)}})
			inheritanceAlterAttributes(s, "leaf", [][]any{{"b", int64(2), false, int64(1)}})
			s.values("SELECT b FROM fresh", []string{"b"}, []string{"INT4"}, [][]any{{int64(30)}})
		})
	})
	t.Run("WholeParentDependencyClosure", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE q(a INT)", 0)
			s.exec("CREATE TABLE c(a INT)", 0)
			s.exec("ALTER TABLE c INHERIT p", 0)
			s.fail("DROP TABLE p", inheritanceDropDiagnostic("p", "c"))
			s.exec("ALTER TABLE c INHERIT q", 0)
			s.exec("CREATE TABLE g() INHERITS(c)", 0)
			s.exec("CREATE TABLE sibling() INHERITS(q)", 0)
			s.exec("INSERT INTO sibling VALUES(8)", 1)
			s.exec("DROP TABLE p CASCADE", 0)
			s.absent("p")
			s.absent("c")
			s.absent("g")
			s.values("SELECT a FROM q", []string{"a"}, []string{"INT4"}, [][]any{{int64(8)}})
			s.edges("sibling", "q")
		})
	})
}
