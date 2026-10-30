package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"regexp"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLAggOrdering(t *testing.T) {
	xmlAggScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
		run(fixture.Exec("CREATE TABLE xa(k int,x xml)",0))
		run(fixture.Exec("INSERT INTO xa VALUES(2,'<b/>'),(NULL,'<n/>'),(1,'<a/>'),(3,NULL)",4))
		for _, tc := range []struct{ order, want string }{{"k","<a/><b/><n/>"},{"k DESC","<n/><b/><a/>"},{"k DESC NULLS LAST","<b/><a/><n/>"}} {
			xmlAggBoth(t,p,b,c,xmlAggSelect("SELECT xmlagg(x ORDER BY "+tc.order+") AS v FROM xa","XML",tc.want))
		}
		xmlAggBoth(t,p,b,c,fixture.Select("SELECT xmlagg(x ORDER BY k) AS a,xmlagg(x ORDER BY k DESC) AS b FROM xa",[]string{"a","b"},[]string{"XML","XML"},[][]any{{"<a/><b/><n/>","<n/><b/><a/>"}}))
		xmlAggBoth(t,p,b,c,fixture.Select("SELECT k,xmlagg(x ORDER BY x::text DESC) AS v FROM xa GROUP BY k ORDER BY k DESC NULLS LAST",[]string{"k","v"},[]string{"INT4","XML"},[][]any{{int64(3),nil},{int64(2),"<b/>"},{int64(1),"<a/>"},{nil,"<n/>"}}))
		run(fixture.Exec("INSERT INTO xa VALUES(1,'<c/>'),(1,'<a/>')",2))
		xmlAggBoth(t,p,b,c,xmlAggSelect("SELECT xmlagg(x ORDER BY k,x::text DESC) AS v FROM xa","XML","<c/><a/><a/><b/><n/>"))
		// Unspecified order is a bag, including duplicate multiplicity. This is
		// deliberately not a backend-to-backend serialized-string comparison.
		for _, q := range []string{"SELECT xmlagg(x) AS v FROM xa","SELECT xmlagg(x ORDER BY 1) AS v FROM xa"} {
			for _, prepared := range []bool{false,true} {
				ctx := context.WithValue(p.Ctx,fixture.RecorderKey{},b.Recorder)
				var rows *sql.Rows
				var err error
				if prepared {
					stmt,e := c.PrepareContext(ctx,q); if e != nil { t.Fatal(e) }
					defer stmt.Close(); rows,err=stmt.QueryContext(ctx)
				} else { rows,err=c.QueryContext(ctx,q) }
				if err != nil { t.Fatal(err) }
				rangeAggregateMetadata(t,rows,b.PG,[]string{"XML"})
				if !rows.Next() { t.Fatal("missing aggregate row") }
				var text string; if err := rows.Scan(&text); err != nil { t.Fatal(err) }
				pieces := regexp.MustCompile(`<[^<>]+/>`).FindAllString(text,-1)
				bag:=map[string]int{}; for _, piece:=range pieces { bag[piece]++ }
				if len(text)!=len("<a/><a/><b/><c/><n/>") || !reflect.DeepEqual(bag,map[string]int{"<a/>":2,"<b/>":1,"<c/>":1,"<n/>":1}) { t.Fatalf("unordered bag %q %#v",text,bag) }
				if rows.Next() || rows.Err()!=nil { t.Fatal("extra rows or error",rows.Err()) }; rows.Close()
			}
		}
	})
}
