package xml

import (
 "context"
 "errors"
 "reflect"
 "testing"
)

func TestXMLDTDParameterCycle(t *testing.T) {
 // The pinned boundary capture (PostgreSQL18.6/180006 UTF8/C, libxml2
 // 2.9.14+dfsg-1.3~deb12u6) supplies this complete single-block Detail.
 // Unlike the original corpus's raw-percent "parameter-cycle" label, both
 // declarations here succeed; the stored replacements cycle on actual use.
 input:=`<!DOCTYPE a [<!ENTITY % p "&#37;q;"><!ENTITY % q "&#37;p;">%p;]><a/>`
 detail:="line 1: Detected an entity reference loop\n%p;\n   ^"
 // Source fields are source-derived: inputNr41 is the q replacement, with
 // its attempted %p; fully consumed before the next PE push is rejected.
 diagnostic:=Diagnostic{Condition:"XML_ERR_ENTITY_LOOP",Domain:ParserDomain,Severity:Fatal,Line:1,Column:4,Offset:3,SourceBaseOffset:0,Message:"Detected an entity reference loop",SourceContext:"%p;",Caret:"   ^",Source:EntitySource,EntityName:"q"}
 eager:=`<!DOCTYPE a [<!ENTITY % p '<!ENTITY &#37; q "&#37;q;">&#37;q;'>%p;]><a/>`
 for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
  r,d,err:=parse(context.Background(),input,Options{Mode:mode},collect)
  var pe *ParseError
  if !errors.As(err,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("mode %v collect %t: result %+v tree %+v err %v",mode,collect,r,d,err)}
  primary:="invalid XML document";if mode==Content{primary="invalid XML content"}
  if pe.Category!=GrammarError||pe.RequestedMode!=mode||pe.EffectiveMode!=Document||pe.Error()!=primary||pe.Detail()!=detail||!reflect.DeepEqual(pe.Diagnostics,[]Diagnostic{diagnostic}){t.Fatalf("mode %v collect %t: error %+v detail %q diagnostics %+v",mode,collect,pe,pe.Detail(),pe.Diagnostics)}
  // The contrasting q declaration expands before q is registered and binds
  // an empty value. Do not turn that legal eager timing into a graph cycle.
  r,d,err=parse(context.Background(),eager,Options{Mode:mode},collect)
  if err!=nil||!r.valid||r.Original()!=eager||r.RequestedMode()!=mode||r.EffectiveMode()!=Document||len(r.Diagnostics())!=0{t.Fatalf("eager mode %v collect %t: %+v %v",mode,collect,r,err)}
  if collect{if root:=dtdRoot(t,d);len(root.children)!=0{t.Fatalf("eager q produced children: %+v",root.children)}}else if d!=nil{t.Fatal("validation retained a tree")}
 }}
}
