package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func ltreeArraysInputDiagnostics(t *testing.T) {
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  ltreeInstall(t,p,b,c)
  for _,tc:=range []struct{input,typ,code,message,detail string}{
   {"{a,,b}","ltree[]","22P02",`malformed array literal: "{a,,b}"`,`Unexpected "," character.`},
   {"{{a},{b,c}}","lquery[]","22P02",`malformed array literal: "{{a},{b,c}}"`,"Multidimensional arrays must have sub-arrays with matching dimensions."},
   {"{a..b}","ltree[]","42601","ltree syntax error at character 3",""},
   {"{a..b}","lquery[]","42601","lquery syntax error at character 3",""},
  } {
   want:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message);want.Detail=tc.detail
   for _,prefix:=range []string{"","/*é*/ "} {
    query:=prefix+"SELECT "+ltreeArrayLiteral(tc.input,tc.typ)
    literal:=want;literal.Position=ltreeSQLPosition(query,ltreeArrayLiteral(tc.input,tc.typ))
    ltreeError(t,p,b,c,query,false,true,false,literal)
    ltreeError(t,p,b,c,query,true,true,true,literal)
   }
   bound:=want;bound.Context="unnamed portal parameter $1 = '...'"
   query:="SELECT $1::"+tc.typ
   ltreeError(t,p,b,c,query,false,true,false,bound,tc.input)
   ltreeError(t,p,b,c,query,true,true,false,bound,tc.input)
  }
 })
}
