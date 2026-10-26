package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func ltxtqueryBalanced(depth int, spaced bool) string {
	s := "a"
	for range depth {
		if spaced { s = s + " & " + s } else { s = "(" + s + "&" + s + ")" }
	}
	return s
}

func TestReferenceLtxtqueryBoundaries(t *testing.T) {
	word:=strings.Repeat("a",255)
	operands:=make([]string,256)
	for i:=range operands { operands[i]=word }
	full:=strings.Join(operands,"&")
	for _,tc:=range []struct{name,input,want,code,message string}{
		{"word255",word,word,"",""},
		{"word256",word+"a","","22023","operand is too long"},
		{"word65535",strings.Repeat("a",65535),"","22023","operand is too long"},
		{"word65536",strings.Repeat("a",65536),"","22023","word is too long"},
		{"operand_storage",full,strings.Join(operands," & "),"",""},
		{"operand_offset",full+"&a","","22023","value is too big"},
		{"offset_before_length",full+"&"+strings.Repeat("a",256),"","22023","value is too big"},
		{"word_before_offset",full+"&"+strings.Repeat("a",65536),"","22023","word is too long"},
		{"not32",strings.Repeat("!",32)+"a",strings.Repeat("!( ",31)+"!a"+strings.Repeat(" )",31),"",""},
		{"not33",strings.Repeat("!",33)+"a","","XX000","stack too short"},
		{"binary_not31","a&"+strings.Repeat("!",31)+"b","a & "+strings.Repeat("!( ",30)+"!b"+strings.Repeat(" )",30),"",""},
		{"binary_not32","a&"+strings.Repeat("!",32)+"b","","XX000","stack too short"},
		{"parentheses_reset",strings.Repeat("!",32)+"("+strings.Repeat("!",32)+"a)",strings.Repeat("!( ",63)+"!a"+strings.Repeat(" )",63),"",""},
		{"right_offset_ok","b|"+ltxtqueryBalanced(13,false),"b | "+ltxtqueryBalanced(13,true),"",""},
		{"right_offset_overflow","b|"+ltxtqueryBalanced(14,false),"","54000","ltxtquery is too large"},
		{"redundant_nesting",strings.Repeat("(",100)+"a"+strings.Repeat(")",100),"a","",""},
	} {
		t.Run(tc.name,func(t *testing.T) {
			ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				ltreeInstall(t,p,b,c)
				run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
				run(fixture.Exec("CREATE TABLE lx_boundary(id integer,v ltxtquery)",0))
				run(fixture.Exec("INSERT INTO lx_boundary VALUES (1,'a')",1))
				for _,prepared:=range []bool{false,true} {
					if tc.code=="" {
						s:=ltxtquerySelect(b,"SELECT $1::ltxtquery AS v",[]string{"v"},[][]any{{tc.want}},tc.input);s.Prepared=prepared;run(s)
					} else {
						want:=queryIntParameterDiagnostic(tc.code,tc.message,tc.input,1)
						ltreeError(t,p,b,c,"SELECT $1::ltxtquery AS v",prepared,true,false,want,tc.input)
						ltreeError(t,p,b,c,"INSERT INTO lx_boundary VALUES (2,$1)",prepared,false,false,want,tc.input)
					}
				}
				if tc.name=="word256" {
					lit:=queryIntLiteral(tc.input);q:="/*é*/ SELECT "+lit+"::ltxtquery AS v"
					want:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message);want.Position=ltreeSQLPosition(q,lit)
					ltreeError(t,p,b,c,q,false,true,true,want)
				}
				run(ltxtquerySelect(b,"SELECT id,v FROM lx_boundary ORDER BY id",[]string{"id","v"},[][]any{{int64(1),"a"}}))
			})
		})
	}
}
