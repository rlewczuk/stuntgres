package xml

import (
 "context"
 "errors"
 "strings"
 "testing"
)

func dtdDefaultAndIdentifierLimits(t *testing.T) {
 t.Helper()
 // Source-derived production boundaries supplement the actual pinned hard
 // limits in dtd_limits_test.go. Defaults use AttValue(normalize=0), THEN type
 // normalization; explicit tokenized attributes use the normalized accelerator.
 for _,collect:=range []bool{false,true}{
  for _,n:=range []int{50000,50001}{
   s:=`<!DOCTYPE a PUBLIC "`+strings.Repeat("x",n)+`" "s"><a/>`
   r,d,e:=parse(context.Background(),s,Options{Mode:Document},collect)
   if n==50000{if e!=nil||!r.valid{t.Fatal(e)};continue}
   var pe *ParseError;if !errors.As(e,&pe)||r.valid||d!=nil{t.Fatal(r,e)}
   var want []string;for _,m:=range []string{"Name too long: Public ID","PUBLIC, the Public Identifier is missing","DOCTYPE improperly terminated"}{want=append(want,"line 1: "+m+"\n"+strings.Repeat("x",80)+"\n"+strings.Repeat(" ",79)+"^")}
   want=append(want,"line 1: Start tag expected, '<' not found\n"+strings.Repeat("x",78)+"\" \n"+strings.Repeat(" ",79)+"^")
   if pe.Detail()!=strings.Join(want,"\n"){t.Fatal(pe.Detail())}
  }
  for _,n:=range []int{50000,50001}{
   prefix:=`<!DOCTYPE a [<!ENTITY `;s:=prefix+strings.Repeat("a",n)+` "v">]><a/>`
   r,d,e:=parse(context.Background(),s,Options{Mode:Document},collect)
   if n==50000{if e!=nil||!r.valid{t.Fatal(e)};continue}
   var pe *ParseError;if !errors.As(e,&pe)||r.valid||d!=nil{t.Fatal(r,e)}
   var want []string;for _,m:=range []string{"Name too long: Name","xmlParseEntityDecl: no name","internal error: xmlParseInternalSubset: error detected in Markup declaration","DOCTYPE improperly terminated","Start tag expected, '<' not found"}{want=append(want,"line 1: "+m+"\n"+s[:80]+"\n"+strings.Repeat(" ",len(prefix))+"^")}
   if pe.Detail()!=strings.Join(want,"\n"){t.Fatal(pe.Detail())}
  }
  for _,typ:=range []string{"CDATA","NMTOKENS"}{for _,n:=range []int{10000000,10000001}{
   prefix:=`<!DOCTYPE a [<!ATTLIST a x `+typ+` "`;payload:="x";if typ=="NMTOKENS"{payload=" "};s:=prefix+strings.Repeat(payload,n)+`">]><a/>`
   r,d,e:=parse(context.Background(),s,Options{Mode:Document},collect)
   if n==10000000{if e!=nil||!r.valid{t.Fatal(typ,e)};if collect{a:=dtdRoot(t,d);if len(a.attributes)!=1||typ=="NMTOKENS"&&a.attributes[0].value!=""||typ=="CDATA"&&len(a.attributes[0].value)!=n{t.Fatal("default limit normalization")}};continue}
   var pe *ParseError;if !errors.As(e,&pe)||r.valid||d!=nil{t.Fatal(typ,r,e)}
   var want []string;for _,m:=range []string{"AttValue length too long","Attribute default value declaration error","Space required after the attribute default value","internal error: xmlParseInternalSubset: error detected in Markup declaration","DOCTYPE improperly terminated","Start tag expected, '<' not found"}{want=append(want,"line 1: "+m+"\n"+s[:80]+"\n"+strings.Repeat(" ",len(prefix)-1)+"^")}
   if pe.Detail()!=strings.Join(want,"\n"){t.Fatal(typ,pe.Detail())}
  }}
  prefix:=`<!DOCTYPE a [<!ATTLIST a x NMTOKENS #IMPLIED>]><a x="`
  for _,tc:=range []struct{value,want string;ok bool}{
   {strings.Repeat(" ",10000001)+"x","x",true},
   {"x  y"+strings.Repeat(" ",10000001),"x y",true},
   {"x"+strings.Repeat(" ",10000001),"",false},
  }{
   s:=prefix+tc.value+`"/>`;r,d,e:=parse(context.Background(),s,Options{Mode:Document},collect)
   if tc.ok{if e!=nil||!r.valid{t.Fatal(e)};if collect{a:=dtdRoot(t,d);if a.attributes[0].value!=tc.want{t.Fatal("explicit token normalization")}};continue}
   var pe *ParseError;if !errors.As(e,&pe)||r.valid||d!=nil{t.Fatal(r,e)}
   var want []string;for _,m:=range []string{"AttValue length too long","attributes construct error","Couldn't find end of Start Tag a line 1","Extra content at the end of the document"}{want=append(want,"line 1: "+m+"\n"+s[:80]+"\n"+strings.Repeat(" ",len(prefix)-1)+"^")}
   if pe.Detail()!=strings.Join(want,"\n"){t.Fatal(pe.Detail())}
  }
 }
}
