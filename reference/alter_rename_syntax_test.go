//go:build reference

package reference_test

import "testing"

func TestReferenceAlterRenameSyntax(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(7)", 1))
	for _, tc := range []struct{ q, token, position string }{{"ALTER TABLE t RENAME a TO s.b", ".", "28"}, {"ALTER TABLE IF EXISTS missing RENAME TO )", ")", "41"}} {
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: tc.position})
			s.prepared = prepared
			s.errorStage = "execute"
			if prepared {
				s.errorStage = "prepare"
			}
			p.step(t, s)
		}
	}
	for _, q := range []string{"ALTER TABLE ONLY t RENAME a TO b", "ALTER TABLE ONLY(t) RENAME COLUMN b TO c", "ALTER TABLE t* RENAME c TO a"} {
		p.step(t, referencePrepared(referenceExec(q, 0)))
	}
	p.step(t, referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
	for _, target := range []string{"t*", "ONLY t", "ONLY(t)"} {
		for _, prepared := range []bool{false, true} {
			s := referenceExec("ALTER TABLE "+target+" RENAME TO marker_table", 0)
			s.prepared = prepared
			p.step(t, s)
			p.step(t, referenceSelect("SELECT * FROM marker_table", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			p.step(t, referenceExec("ALTER TABLE marker_table RENAME TO t", 0))
		}
	}
	p.step(t, referenceExec("CREATE TABLE if(a INT)", 0))
	p.step(t, referenceExec("ALTER TABLE IF EXISTS if RENAME a TO b", 0))
	p.step(t, referencePrepared(referenceExec("ALTER TABLE if RENAME b TO a", 0)))
	for _, prepared := range []bool{false, true} {
		s := referenceImplicitError(`ALTER TABLE "if" EXISTS t RENAME a TO b`, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "EXISTS"`, Position: "18"})
		s.prepared = prepared
		s.errorStage = "execute"
		if prepared {
			s.errorStage = "prepare"
		}
		p.step(t, s)
	}
}
