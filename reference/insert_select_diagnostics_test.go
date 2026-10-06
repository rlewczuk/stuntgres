//go:build reference

package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

func TestReferenceInsertSelectValuesDiagnostics(t *testing.T) {
	for _, tc := range []struct{ q, code, message, token string }{
		{"INSERT INTO t VALUES(1),(TRUE) ORDER BY 1", "42804", "VALUES types integer and boolean cannot be matched", "TRUE"},
		{"INSERT INTO t VALUES(TRUE),(1) ORDER BY 1", "42804", "VALUES types boolean and integer cannot be matched", "1)"},
		{"INSERT INTO t VALUES(TIME '00:00'),(TIMESTAMP 'epoch') ORDER BY 1", "42846", "VALUES could not convert type timestamp without time zone to time without time zone", "'epoch'"},
		{"INSERT INTO t VALUES(TIMESTAMP 'epoch'),(TIME '00:00') ORDER BY 1", "42846", "VALUES could not convert type time without time zone to timestamp without time zone", "'00:00'"},
		{"INSERT INTO t VALUES(1),(2,3) ORDER BY 1", "42601", "VALUES lists must all be the same length", "2,3"},
		{"INSERT INTO t VALUES(1) ORDER BY 0", "42P10", "ORDER BY position 0 is not in select list", "0"},
		{"INSERT INTO t VALUES(1) ORDER BY 2", "42P10", "ORDER BY position 2 is not in select list", "2"},
		{"INSERT INTO t VALUES(1) ORDER BY 'x'", "42601", "non-integer constant in ORDER BY", "'x'"},
		{"INSERT INTO t VALUES(1) ORDER BY TRUE", "42601", "non-integer constant in ORDER BY", "TRUE"},
		{"INSERT INTO t VALUES(1) ORDER BY NULL", "42601", "non-integer constant in ORDER BY", "NULL"},
		{"INSERT INTO t VALUES(1) ORDER BY 1.5", "42601", "non-integer constant in ORDER BY", "1.5"},
		{`INSERT INTO t VALUES(1) ORDER BY "1"`, "42703", `column "1" does not exist`, `"1"`},
		{"INSERT INTO t(i,b) SELECT $1,$1", "42P08", "inconsistent types deduced for parameter $1", "$1"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE t(i INT,b BOOL)", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(9,TRUE)", 1))
			index := strings.Index(tc.q, tc.token)
			if tc.code == "42P08" {
				index = strings.LastIndex(tc.q, tc.token)
			}
			want := referenceDiagnostic{Code: tc.code, Message: tc.message, Position: strconv.Itoa(index + 1)}
			if tc.code == "42P08" {
				want.Detail = "integer versus boolean"
			}
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError(tc.q, want)
				if tc.code == "42P08" {
					s.args = []any{nil}
				}
				s.prepared = prepared
				s.errorStage = "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				p.step(t, s)
			}
			p.step(t, referenceSelect("SELECT * FROM t", []string{"i", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(9), true}}))
		})
	}
}

func TestReferenceInsertSelectAtomicConstraints(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{referenceExec("CREATE TABLE s(i INT)", 0), referenceExec("INSERT INTO s VALUES(1),(NULL)", 2), referenceExec("CREATE TABLE t(i INT NOT NULL)", 0), referenceExec("INSERT INTO t VALUES(9)", 1)} {
		p.step(t, s)
	}
	for _, prepared := range []bool{false, true} {
		s := referenceImplicitError("INSERT INTO t SELECT i FROM s ORDER BY i", referenceDiagnostic{Code: "23502", Message: `null value in column "i" of relation "t" violates not-null constraint`, Schema: "public", Table: "t", Column: "i", Detail: "Failing row contains (null)."})
		s.prepared = prepared
		s.errorStage = "execute"
		p.step(t, s)
		s = referenceImplicitError("INSERT INTO t SELECT i FROM s WHERE i='bad'", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "39"})
		s.prepared = prepared
		s.errorStage = "execute"
		if prepared {
			s.errorStage = "prepare"
		}
		p.step(t, s)
		p.step(t, referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
	}
}
