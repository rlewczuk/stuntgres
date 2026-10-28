package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreeLtxtqueryBoundaries(t *testing.T){
 word:=strings.Repeat("a",255)
 operands:=make([]string,256);for i:=range operands{operands[i]=word};full:=strings.Join(operands,"&")
 for _,tc:=range []struct{name,path,query string;want bool;code,message string}{
  {"empty","","!foo",true,"",""},
  {"word255",word,word,true,"",""},{"word255_miss",word[:254]+"b",word,false,"",""},
  {"label1000",strings.Repeat("a",1000),word+"*",true,"",""},
  {"labels65535",strings.Repeat("a.",65534)+"z","z",true,"",""},
  {"not32","a",strings.Repeat("!",32)+"a",true,"",""},
  {"operand_storage",word,full,true,"",""},
  {"operand_offset","a",full+"&a",false,"22023","value is too big"},
  {"offset_before_length","a",full+"&"+strings.Repeat("a",256),false,"22023","value is too big"},
  {"word65535","a",strings.Repeat("a",65535),false,"22023","operand is too long"},
  {"word65536","a",strings.Repeat("a",65536),false,"22023","word is too long"},
  {"word_before_offset","a",full+"&"+strings.Repeat("a",65536),false,"22023","word is too long"},
  {"binary_not31","a","a&"+strings.Repeat("!",31)+"b",true,"",""},
  {"binary_not32","a","a&"+strings.Repeat("!",32)+"b",false,"XX000","stack too short"},
  {"parentheses_reset","a",strings.Repeat("!",32)+"("+strings.Repeat("!",32)+"a)",true,"",""},
  {"right_offset_ok","a","b|"+ltxtqueryBalanced(13,false),true,"",""},
  {"right_offset_overflow","a","b|"+ltxtqueryBalanced(14,false),false,"54000","ltxtquery is too large"},
 }{t.Run(tc.name,func(t *testing.T){
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   ltreeInstall(t,p,b,c)
   for _,form:=range ltreeLtxtqueryForms("$1::ltree","$2::ltxtquery"){
    q:="SELECT "+form+" AS v"
    if tc.code==""{s:=ltreeArrayResult(b,q,tc.want,tc.path,tc.query);ltreeLtxtqueryRun(t,p,b,c,s);ltreeLtxtqueryRun(t,p,b,c,fixture.Prepared(s));continue}
    want:=queryIntParameterDiagnostic(tc.code,tc.message,tc.query,2)
    for _,prepared:=range []bool{false,true}{ltreeError(t,p,b,c,q,prepared,true,false,want,tc.path,tc.query)}
    // Strictness never erases the selected query's conversion diagnostic.
    ltreeError(t,p,b,c,q,true,true,false,want,nil,tc.query)
   }
   if tc.code!=""{
    lit:=queryIntLiteral(tc.query);q:="/*é*/ SELECT 'a'::ltree @ "+lit+"::ltxtquery AS v"
    want:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message);want.Position=ltreeSQLPosition(q,lit)
    for _,prepared:=range []bool{false,true}{ltreeError(t,p,b,c,q,prepared,true,true,want)}
   }
  })
 })}
}
