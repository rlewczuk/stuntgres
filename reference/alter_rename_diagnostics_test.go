//go:build reference

package reference_test

import "testing"

func TestReferenceAlterRenameDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT,b TEXT,oid INT)", 0))
	p.step(t, referenceExec("CREATE TABLE u(a INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(7,'keep',9)", 1))
	cases := []struct{ q, code, message string }{
		{"ALTER TABLE t RENAME missing TO b", "42703", `column "missing" does not exist`},
		{"ALTER TABLE t RENAME a TO b", "42701", `column "b" of relation "t" already exists`},
		{"ALTER TABLE t RENAME a TO a", "42701", `column "a" of relation "t" already exists`},
		{"ALTER TABLE t RENAME TO u", "42P07", `relation "u" already exists`},
		{"ALTER TABLE t RENAME TO t", "42P07", `relation "t" already exists`},
		{`ALTER TABLE t RENAME "q""u\z" TO b`, "42703", `column "q"u\z" does not exist`},
		{"ALTER TABLE missing RENAME TO u", "42P01", `relation "missing" does not exist`},
	}
	for _, system := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
		cases = append(cases, struct{ q, code, message string }{"ALTER TABLE t RENAME " + system + " TO b", "0A000", `cannot rename system column "` + system + `"`}, struct{ q, code, message string }{"ALTER TABLE t RENAME a TO " + system, "42701", `column name "` + system + `" conflicts with a system column name`})
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message})
			s.prepared = prepared
			s.errorStage = "execute"
			p.step(t, s)
			p.step(t, referenceSelect("SELECT * FROM t", []string{"a", "b", "oid"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(7), "keep", int64(9)}}))
		}
	}
	p.step(t, referenceExec("ALTER TABLE t RENAME oid TO ordinary", 0))
}
