package xml

import (
 "context"
 "testing"
)

// XML 1.0 sections 2.11/4.4.2 and pinned xmlParseReference distinguish
// literal input line endings from already-decoded character references.
func TestXMLDTDCharacterReferences(t *testing.T) {
 for _,tc:=range []struct{name,declarations,body,want string}{
  {"decimal CR","","&#13;","\r"},
  {"hexadecimal CR","","&#xD;","\r"},
  {"adjacent references","","&#13;&#10;","\r\n"},
  {"mixed references","","x&#xD;&#xA;y","x\r\ny"},
  {"literal CR","","\r","\n"},
  {"literal CRLF","","\r\n","\n"},
  {"literal plus reference","","\r&#10;","\n\n"},
  {"GE decimal reference",`<!ENTITY e "&#38;#13;">`,"&e;","\r"},
  {"GE hexadecimal reference",`<!ENTITY e "&#38;#xD;">`,"&e;","\r"},
  {"GE adjacent references",`<!ENTITY e "&#38;#13;&#38;#10;">`,"&e;","\r\n"},
  // EntityValue numeric references are decoded eagerly: these CR bytes
  // subsequently enter balanced replacement parsing as literal source.
  {"GE eager numeric CR",`<!ENTITY e "&#13;">`,"&e;","\n"},
  {"GE eager numeric CRLF",`<!ENTITY e "&#13;&#10;">`,"&e;","\n"},
  {"GE literal CRLF","<!ENTITY e \"\r\n\">","&e;","\n"},
 }{t.Run(tc.name,func(t *testing.T){
  input:="<!DOCTYPE a ["+tc.declarations+"]><a>"+tc.body+"</a>"
  for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
   r,d,err:=parse(context.Background(),input,Options{Mode:mode},collect)
   if err!=nil||!r.valid||r.Original()!=input||r.RequestedMode()!=mode||r.EffectiveMode()!=Document||len(r.Diagnostics())!=0{t.Fatalf("mode %v collect %t: result %+v err %v",mode,collect,r,err)}
   if !collect{if d!=nil{t.Fatal("validation retained a tree")};continue}
   root:=dtdRoot(t,d)
   if len(root.children)!=1||root.children[0].kind!=textNode||root.children[0].value!=tc.want{t.Fatalf("mode %v: children %+v want text %q",mode,root.children,tc.want)}
  }}
 })}
}
