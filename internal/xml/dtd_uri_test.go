package xml

import (
 "context"
 "errors"
 "strings"
 "testing"
)

func dtdURILiterals(t *testing.T) {
 t.Helper()
 // uri.c's RFC3986 productions are the authority here, including its opaque
 // bracket-host scan and signed-int port boundary, not a host URL parser.
 for _,id:=range []string{"","relative.dtd","a/b:c","urn:word","file:///x","http://user:pass@host:2147483647/path?x=y","http://[]/","http://[opaque host]/"}{
  s:=`<!DOCTYPE a [<!ENTITY e SYSTEM "`+id+`">]><a>x&e;y</a>`
  r,d,e:=parseExpanded(context.Background(),s,Options{Mode:Document});if e!=nil||!r.valid||r.Original()!=s{t.Fatal(id,r,e)}
  a:=dtdRoot(t,d);if len(a.children)!=1||a.children[0].value!="xy"{t.Fatal(id,a)}
 }
 for _,tc:=range []struct{id,message string;condition Condition;level Severity}{
  {"bad url","Invalid URI: bad url","XML_ERR_INVALID_URI",Error},
  {"bad url#frag","Invalid URI: bad url#frag","XML_ERR_INVALID_URI",Error},
  {"x%Q0","Invalid URI: x%Q0","XML_ERR_INVALID_URI",Error},
  {"http://host:","Invalid URI: http://host:","XML_ERR_INVALID_URI",Error},
  {"http://host:2147483648/","Invalid URI: http://host:2147483648/","XML_ERR_INVALID_URI",Error},
  {"http://1.2.3.4x/","Invalid URI: http://1.2.3.4x/","XML_ERR_INVALID_URI",Error},
  {"x#frag","Fragment not allowed","XML_ERR_URI_FRAGMENT",Fatal},
  {"x#","Fragment not allowed","XML_ERR_URI_FRAGMENT",Fatal},
 }{for _,collect:=range []bool{false,true}{
  prefix:=`<!DOCTYPE a [<!ENTITY e SYSTEM "`;s:=prefix+tc.id+`">]><a/>`;at:=len(prefix)+len(tc.id)+1
  r,d,e:=parse(context.Background(),s,Options{Mode:Document},collect);var pe *ParseError
  if !errors.As(e,&pe)||r.valid||d!=nil{t.Fatal(tc.id,r,e)}
  want:="line 1: "+tc.message+"\n"+s+"\n"+strings.Repeat(" ",at)+"^"
  if pe.Detail()!=want||len(pe.Diagnostics)!=1||pe.Diagnostics[0].Condition!=tc.condition||pe.Diagnostics[0].Severity!=tc.level{t.Fatalf("%s: %s",tc.id,pe.Detail())}
 }}
}
