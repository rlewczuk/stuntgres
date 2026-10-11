package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterRenameSyntax(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(a INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(7)", 1))
	for _, tc := range []struct{ q, token, position string }{{"ALTER TABLE t RENAME a TO s.b", ".", "28"}, {"ALTER TABLE IF EXISTS missing RENAME TO )", ")", "41"}} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: tc.position})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			p.Step(t, s)
		}
	}
	for _, q := range []string{"ALTER TABLE ONLY t RENAME a TO b", "ALTER TABLE ONLY(t) RENAME COLUMN b TO c", "ALTER TABLE t* RENAME c TO a"} {
		p.Step(t, fixture.Prepared(fixture.Exec(q, 0)))
	}
	p.Step(t, fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
	p.Step(t, fixture.Exec("CREATE TABLE if(a INT)", 0))
	p.Step(t, fixture.Exec("ALTER TABLE IF EXISTS if RENAME a TO b", 0))
	p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE if RENAME b TO a", 0)))
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(`ALTER TABLE "if" EXISTS t RENAME a TO b`, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "EXISTS"`, Position: "18"})
		s.Prepared = prepared
		s.ErrorStage = "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		p.Step(t, s)
	}
}
