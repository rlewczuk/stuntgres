package xml

import (
 "context"
 "errors"
 "reflect"
 "strings"
 "testing"
)

func TestXMLDTDDiagnosticFrames(t *testing.T) {
 input:=`<!DOCTYPE a [<!ENTITY e "<b>">]><a>&e;</a>`
 for _,collect:=range []bool{false,true}{r,d,e:=parse(context.Background(),input,Options{Mode:Document},collect);var pe *ParseError
  if !errors.As(e,&pe)||r.valid||d!=nil{t.Fatal(r,e)}
  // Text/carets are actual pinned observations. Symbolic identities, source
  // provenance and local structured coordinates follow the matching parser.c.
  want:=[]Diagnostic{
   {Condition:"XML_ERR_TAG_NOT_FINISHED",Domain:ParserDomain,Severity:Fatal,Line:1,Column:4,Offset:3,Message:"Premature end of data in tag b line 1",SourceContext:"<b>",Caret:"   ^",Source:EntitySource,EntityName:"e"},
   {Condition:"XML_ERR_UNDECLARED_ENTITY",Domain:ParserDomain,Severity:Fatal,Line:1,Column:39,Offset:38,Message:"Entity 'e' failed to parse",SourceContext:input,Caret:strings.Repeat(" ",38)+"^"},
  }
  if !reflect.DeepEqual(pe.Diagnostics,want){t.Fatalf("%+v want %+v",pe.Diagnostics,want)}
 }
}
func TestXMLDTDSourceBoundaries(t *testing.T) {
 // Input-identity requirements are source-derived parser.c 4856, 5200,
 // 5365, 5609, 6070 and 6425; no malformed expansion may borrow its caller's
 // closing markup. The ordinary complete-PE cases remain legal.
 for _,s:=range []string{
  `<!DOCTYPE a [<!ENTITY % p "<!--hello">%p;-->]><a/>`,
  `<!DOCTYPE a [<!ENTITY % p "<?go hello">%p;?>]><a/>`,
  `<!DOCTYPE a [<!ENTITY % p "<!ATTLIST a x CDATA 'v'">%p;>]><a/>`,
  `<!DOCTYPE a [<!ENTITY % p "<!NOTATION n PUBLIC 'id'">%p;>]><a/>`,
  `<!DOCTYPE a [<!ENTITY % p "<!ENTITY e 'v'">%p;>]><a/>`,
 }{for _,collect:=range []bool{false,true}{r,d,e:=parse(context.Background(),s,Options{Mode:Document},collect);var pe *ParseError;if !errors.As(e,&pe)||r.valid||d!=nil{t.Fatal(s,r,e)};found:=false;for _,v:=range pe.Diagnostics{if v.Condition=="XML_ERR_ENTITY_BOUNDARY"{found=true}};if !found{t.Fatalf("missing source boundary: %s: %s",s,pe.Detail())}}}
}
