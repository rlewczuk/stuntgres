package xml

import (
 "context"
 "errors"
 "reflect"
 "strings"
 "testing"
)

func TestXMLDTDNDATASeparators(t *testing.T) {
 // Independent PostgreSQL18.6/180006 UTF8/C captures, Debian libxml2
 // 2.9.14+dfsg-1.3~deb12u6, precede this remedy. SYSTEM/PUBLIC in both
 // requested modes: missing before => one block; missing both => two ordered
 // blocks; space/TAB/LF before => accepted despite undeclared notation n.
 // All captures had empty warning stderr. Structured fields are source-derived.
 for _,id:=range []struct{spelling,publicID string;offset int}{
  {`SYSTEM "x"`, "",34},
  {`PUBLIC "id" "x"`, "id",39},
 }{for _,tc:=range []struct{name,before,after string;messages []string}{
  {"missing before",""," ",[]string{"Space required before 'NDATA'"}},
  {"space"," "," ",nil},
  {"TAB","\t"," ",nil},
  {"LF","\n"," ",nil},
  {"missing both","","",[]string{"Space required before 'NDATA'","Space required after 'NDATA'"}},
 }{for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
  input:=`<!DOCTYPE a [<!ENTITY e `+id.spelling+tc.before+`NDATA`+tc.after+`n>]><a/>`
  r,d,err:=parse(context.Background(),input,Options{Mode:mode},collect)
  if len(tc.messages)==0{
   if err!=nil||!r.valid||r.Original()!=input||r.RequestedMode()!=mode||r.EffectiveMode()!=Document||len(r.Diagnostics())!=0{t.Fatalf("%s %s mode %v collect %t: %+v %v",id.spelling,tc.name,mode,collect,r,err)}
   if collect{
    root:=dtdRoot(t,d);if root.name!="a"||len(root.children)!=0||len(root.attributes)!=0{t.Fatalf("unused entity affected root: %+v",root)}
    declarations:=d.nodes[0].doctype.declarations
    if len(declarations)!=1{t.Fatalf("declarations %+v",declarations)}
    entity:=declarations[0]
    if entity.kind!="ENTITY"||entity.name!="e"||!entity.external||!entity.unparsed||entity.parameter||entity.systemID!="x"||entity.publicID!=id.publicID||entity.notation!="n"{t.Fatalf("external NDATA identity %+v",entity)}
   }else if d!=nil{t.Fatal("validation retained a tree")}
  }else{
   var pe *ParseError
   if !errors.As(err,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("%s %s mode %v collect %t: %+v tree %+v err %v",id.spelling,tc.name,mode,collect,r,d,err)}
   var diagnostics []Diagnostic;var blocks []string
   for i,message:=range tc.messages{
    offset:=id.offset+i*5;caret:=strings.Repeat(" ",offset)+"^"
    diagnostics=append(diagnostics,Diagnostic{Condition:"XML_ERR_SPACE_REQUIRED",Domain:ParserDomain,Severity:Fatal,Line:1,Column:offset+1,Offset:offset,SourceBaseOffset:0,Message:message,SourceContext:input,Caret:caret,Source:OriginalSource})
    blocks=append(blocks,"line 1: "+message+"\n"+input+"\n"+caret)
   }
   primary:="invalid XML document";if mode==Content{primary="invalid XML content"}
   if pe.Category!=GrammarError||pe.RequestedMode!=mode||pe.EffectiveMode!=Document||pe.Error()!=primary||pe.Detail()!=strings.Join(blocks,"\n")||!reflect.DeepEqual(pe.Diagnostics,diagnostics){t.Fatalf("%s %s mode %v collect %t: %+v Detail %q diagnostics %+v",id.spelling,tc.name,mode,collect,pe,pe.Detail(),pe.Diagnostics)}
  }
  accept(t,"<healthy/>",mode)
 }}}}
}

func TestXMLDTDNDATAMissingName(t *testing.T) {
 // Independent PostgreSQL18.6/180006 UTF8/C with Debian libxml2
 // 2.9.14+dfsg-1.3~deb12u6 accepts unused missing/named NDATA declarations.
 // Both content and attribute uses report exactly the same UNPARSED identity.
 // All captured warning streams are empty. Symbolic/frame fields and the
 // transient unparsed kind are source-derived, not exposed SQL struct fields.
 for _,id:=range []struct{spelling,publicID string;originalLength,contentOffset,attributeOffset int}{
  {`SYSTEM "x"`,"",48,50,53},
  {`PUBLIC "id" "x"`,"id",53,55,58},
 }{for _,notation:=range []string{"","n"}{for _,body:=range []struct{input string;used bool;offset int}{
  {"<a/>",false,0},
  {"<a>&e;</a>",true,id.contentOffset},
  {`<a x="&e;"/>`,true,id.attributeOffset},
 }{for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
  declaration:=`<!ENTITY e `+id.spelling+` NDATA `+notation+`>`
  doctypeRaw:=`<!DOCTYPE a [`+declaration+`]>`
  input:=doctypeRaw+body.input
  r,d,err:=parse(context.Background(),input,Options{Mode:mode},collect)
  if !body.used{
   if err!=nil||!r.valid||r.Original()!=input||len(r.Original())!=id.originalLength+len(notation)||r.RequestedMode()!=mode||r.EffectiveMode()!=Document||len(r.Diagnostics())!=0{t.Fatalf("%s notation %q mode %v collect %t: %+v %v",id.spelling,notation,mode,collect,r,err)}
   output,warning,outputErr:=OutputText(context.Background(),r)
   if outputErr!=nil||warning!=(OutputWarning{})||output!=input{t.Fatalf("original output %q warning %+v err %v",output,warning,outputErr)}
   if collect{
    if d==nil||len(d.nodes)!=2||d.nodes[0].kind!=doctypeNode||d.nodes[1].kind!=elementNode{t.Fatalf("transient document %+v",d)}
    root:=dtdRoot(t,d)
    if root.name!="a"||len(root.attributes)!=0||len(root.namespaces)!=0||len(root.children)!=0{t.Fatalf("unused declaration added defaults/body: %+v",root)}
    dt:=d.nodes[0].doctype
    if dt==nil||dt.raw!=doctypeRaw||len(dt.declarations)!=1{t.Fatalf("doctype identity %+v",dt)}
    entity:=dt.declarations[0]
    if entity.kind!="ENTITY"||entity.name!="e"||entity.raw!=declaration||entity.value!=""||!entity.external||!entity.unparsed||entity.parameter||entity.notation!=notation||entity.systemID!="x"||entity.publicID!=id.publicID{t.Fatalf("NDATA identity %+v",entity)}
   }else if d!=nil{t.Fatal("validation retained a tree")}
  }else{
   var pe *ParseError
   if !errors.As(err,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("%s notation %q body %s mode %v collect %t: %+v tree %+v err %v",id.spelling,notation,body.input,mode,collect,r,d,err)}
   offset:=body.offset+len(notation)
   message:="Entity reference to unparsed entity e";caret:=strings.Repeat(" ",offset)+"^"
   detail:="line 1: "+message+"\n"+input+"\n"+caret
   diagnostic:=Diagnostic{Condition:"XML_ERR_UNPARSED_ENTITY",Domain:ParserDomain,Severity:Fatal,Line:1,Column:offset+1,Offset:offset,SourceBaseOffset:0,Message:message,SourceContext:input,Caret:caret,Source:OriginalSource}
   primary:="invalid XML document";if mode==Content{primary="invalid XML content"}
   if pe.Category!=GrammarError||pe.RequestedMode!=mode||pe.EffectiveMode!=Document||pe.Error()!=primary||pe.Detail()!=detail||!reflect.DeepEqual(pe.Diagnostics,[]Diagnostic{diagnostic}){t.Fatalf("%s notation %q body %s mode %v collect %t: %+v Detail %q diagnostics %+v",id.spelling,notation,body.input,mode,collect,pe,pe.Detail(),pe.Diagnostics)}
  }
  accept(t,"<healthy/>",mode)
 }}}}}
}
