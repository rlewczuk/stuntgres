//go:build reference

package reference_test

import "testing"

func TestReferenceBinarySliceCheckDeparse(t *testing.T) {
	p := newReferencePair(t)
	for _, tc := range []struct{ name, expr, compact, pretty string }{
		{"os", "overlay(v PLACING s FROM i FOR n)=e", "(OVERLAY(v PLACING s FROM i FOR n) = e)", "OVERLAY(v PLACING s FROM i FOR n) = e"},
		{"of", "overlay(v,s,i,n)=e", "(\"overlay\"(v, s, i, n) = e)", "\"overlay\"(v, s, i, n) = e"},
		{"p", "position(s IN v)>0", "(POSITION((s) IN (v)) > 0)", "POSITION((s) IN (v)) > 0"},
		{"sf", "substring(v FROM i FOR n)=e", "(SUBSTRING(v FROM i FOR n) = e)", "SUBSTRING(v FROM i FOR n) = e"},
		{"sr", "substring(v FOR n FROM i)=e", "(SUBSTRING(v FROM i FOR n) = e)", "SUBSTRING(v FROM i FOR n) = e"},
		{"sn", "substring(v FOR n)=e", "(SUBSTRING(v FROM 1 FOR n) = e)", "SUBSTRING(v FROM 1 FOR n) = e"},
		{"sub", "substr(v,i,n)=e", "(substr(v, i, n) = e)", "substr(v, i, n) = e"},
		{"bt", "trim(s FROM v)=e", "(TRIM(BOTH s FROM v) = e)", "TRIM(BOTH s FROM v) = e"},
		{"lt", "trim(LEADING s FROM v)=e", "(TRIM(LEADING s FROM v) = e)", "TRIM(LEADING s FROM v) = e"},
		{"rt", "trim(TRAILING v,s)=e", "(TRIM(TRAILING s FROM v) = e)", "TRIM(TRAILING s FROM v) = e"},
	} {
		p.step(t, referenceExec("CREATE TABLE bs_"+tc.name+"(v BYTEA,s BYTEA,i INTEGER,n INTEGER,e BYTEA,CONSTRAINT ck_"+tc.name+" CHECK("+tc.expr+"))", 0))
		for _, mode := range []struct{ flag, want string }{{"false", tc.compact}, {"true", tc.pretty}} {
			for _, prepared := range []bool{false, true} {
				expr := referenceSelect("SELECT pg_get_expr(conbin,conrelid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{mode.want}})
				def := referenceSelect("SELECT pg_get_constraintdef(oid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + mode.want + ")"}})
				if prepared {
					expr = referencePrepared(expr)
					def = referencePrepared(def)
				}
				p.step(t, expr)
				p.step(t, def)
			}
		}
	}
	p.step(t, referenceExec("INSERT INTO bs_os VALUES('abcdef','XY',3,2,'abXYef')", 1))
	p.step(t, referenceSelect("SELECT overlay(v PLACING s FROM i FOR n) AS o FROM bs_os", []string{"o"}, []string{"BYTEA"}, [][]any{{"abXYef"}}))
	p.step(t, referenceExec("INSERT INTO bs_bt VALUES('xxabcxx','x',1,1,'abc')", 1))
	p.step(t, referenceSelect("SELECT trim(s FROM v) AS b FROM bs_bt", []string{"b"}, []string{"BYTEA"}, [][]any{{"abc"}}))
}
