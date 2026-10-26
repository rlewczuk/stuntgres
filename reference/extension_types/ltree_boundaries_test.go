package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLtreeBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, input string }{
		{"empty", ""}, {"label999", strings.Repeat("x",999)}, {"label1000", strings.Repeat("x",1000)},
		{"labels65534", strings.Repeat("a.",65533)+"a"}, {"labels65535", strings.Repeat("a.",65534)+"a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ltreeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				ltreeInstall(t,p,b,c)
				run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
				run(fixture.Exec("CREATE TABLE lt_boundary(id integer,v ltree)",0))
				run(fixture.Exec("INSERT INTO lt_boundary VALUES (1,"+queryIntLiteral(tc.input)+")",1))
				run(fixture.Prepared(fixture.Exec("INSERT INTO lt_boundary VALUES (2,$1)",1,tc.input)))
				run(ltreeSelect(b,"SELECT id,v FROM lt_boundary ORDER BY id",[]string{"id","v"},[][]any{{int64(1),tc.input},{int64(2),tc.input}}))
			})
		})
	}
	for i, tc := range []struct{ input, code, message, detail string }{
		{strings.Repeat("x",1001),"42622","label string is too long","Label length is 1001, must be at most 1000, at character 1002."},
		{"a."+strings.Repeat("x",1001),"42622","label string is too long","Label length is 1001, must be at most 1000, at character 1004."},
		{strings.Repeat("x",1001)+".a","42622","label string is too long","Label length is 1001, must be at most 1000, at character 1002."},
		{strings.Repeat("x",1001)+"/","42601","ltree syntax error at character 1002",""},
		{strings.Repeat("x",1001)+"./","42622","label string is too long","Label length is 1001, must be at most 1000, at character 1002."},
		{strings.Repeat("a.",65535)+"a","54000","number of ltree labels (65536) exceeds the maximum allowed (65535)",""},
		{strings.Repeat(".",65535),"54000","number of ltree labels (65536) exceeds the maximum allowed (65535)",""},
		{"/"+strings.Repeat(".",65535),"54000","number of ltree labels (65536) exceeds the maximum allowed (65535)",""},
	} {
		t.Run(fmt.Sprintf("failure%02d",i),func(t *testing.T) {
			ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				ltreeInstall(t,p,b,c)
				for _, prepared := range []bool{false,true} {
					q := "SELECT "+queryIntLiteral(tc.input)+"::ltree AS v"
					want := fixture.ControlsReferenceDiagnostic(tc.code,tc.message)
					want.Detail, want.Position = tc.detail,"8"
					ltreeError(t,p,b,c,q,prepared,true,true,want)
					want = queryIntParameterDiagnostic(tc.code,tc.message,tc.input,1); want.Detail=tc.detail
					ltreeError(t,p,b,c,"SELECT $1::ltree AS v",prepared,true,false,want,tc.input)
				}
			})
		})
	}
}
