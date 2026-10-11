package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceAlterNullabilityWholeInput(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(7,9)", 1))
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL,", "CREATE TABLE prefix(v INT);INSERT INTO prefix VALUES(1);ALTER TABLE t ALTER a SET NOT NULL,"} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: strconv.Itoa(utf8.RuneCountInString(q) + 1)})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			p.Step(t, s)
		}
	}
	p.Step(t, fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
	p.Step(t, fixture.ImplicitError("SELECT * FROM prefix", fixture.Diagnostic{Code: "42P01", Message: `relation "prefix" does not exist`, Position: "15"}))
}

func TestReferenceAlterNullabilityDependencyAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(7,9)", 1))
	p.Step(t, fixture.Exec("CREATE VIEW v AS SELECT b FROM t", 0))
	p.Step(t, fixture.Exec("CREATE SEQUENCE owned OWNED BY t.b", 0))
	p.Step(t, fixture.ImplicitError("ALTER TABLE t DROP COLUMN b RESTRICT, ALTER a SET NOT NULL", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column b of table t because other objects depend on it", Detail: "view v depends on column b of table t", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
	p.Step(t, fixture.ImplicitError("ALTER TABLE t DROP COLUMN b CASCADE, ALTER missing SET NOT NULL", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`}))
	p.Step(t, fixture.Select("SELECT * FROM v", []string{"b"}, []string{"INT4"}, [][]any{{int64(9)}}))
	p.Step(t, fixture.Select("SELECT nextval('owned')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
	p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE t DROP COLUMN b CASCADE, ALTER a SET NOT NULL", 0)))
	p.Step(t, fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
	p.Step(t, fixture.ImplicitError("ALTER SEQUENCE owned RESTART", fixture.Diagnostic{Code: "42P01", Message: `relation "owned" does not exist`}))
}
