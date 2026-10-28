package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinarySliceCheckDeparse(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
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
				apply(t, fixture.Exec("CREATE TABLE bs_"+tc.name+"(v BYTEA,s BYTEA,i INTEGER,n INTEGER,e BYTEA,CONSTRAINT ck_"+tc.name+" CHECK("+tc.expr+"))", 0))
				for _, mode := range []struct{ flag, want string }{{"false", tc.compact}, {"true", tc.pretty}} {
					for _, prepared := range []bool{false, true} {
						expr := fixture.Select("SELECT pg_get_expr(conbin,conrelid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{mode.want}})
						def := fixture.Select("SELECT pg_get_constraintdef(oid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + mode.want + ")"}})
						if prepared {
							expr = fixture.Prepared(expr)
							def = fixture.Prepared(def)
						}
						apply(t, expr)
						apply(t, def)
					}
				}
			}
			apply(t, fixture.Exec("INSERT INTO bs_os VALUES('abcdef','XY',3,2,'abXYef')", 1))
			apply(t, fixture.Select("SELECT overlay(v PLACING s FROM i FOR n) AS o FROM bs_os", []string{"o"}, []string{"BYTEA"}, [][]any{{"abXYef"}}))
			apply(t, fixture.Exec("INSERT INTO bs_bt VALUES('xxabcxx','x',1,1,'abc')", 1))
			apply(t, fixture.Select("SELECT trim(s FROM v) AS b FROM bs_bt", []string{"b"}, []string{"BYTEA"}, [][]any{{"abc"}}))
		})
	}
}
