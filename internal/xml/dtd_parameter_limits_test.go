package xml

import (
 "context"
 "errors"
 "reflect"
 "strings"
 "testing"
)

func TestXMLDTDParameterReferenceLimit(t *testing.T) {
 // Independently captured before the producer fix: PostgreSQL18.6/180006,
 // UTF8/C, Debian libxml2 2.9.14+dfsg-1.3~deb12u6. pg_input_error_info
 // accepts 10000 and returns this sole Detail at 10001 in both modes;
 // all four capture children have empty warning stderr. Coordinates/source
 // identity below follow the matching pinned input-PE/accounting productions.
 source:="missing;%missing;%missing;%missing;%missing;%missing;%missing;%missing;%missing;"
 caret:=strings.Repeat(" ",79)+"^"
 detail:="line 1: Detected an entity reference loop\n"+source+"\n"+caret
 diagnostic:=Diagnostic{Condition:"XML_ERR_ENTITY_LOOP",Domain:ParserDomain,Severity:Fatal,Line:1,Column:90045,Offset:90044,SourceBaseOffset:0,Message:"Detected an entity reference loop",SourceContext:source,Caret:caret,Source:OriginalSource}
 for _,count:=range []int{10000,10001}{for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
  input:=`<!DOCTYPE a SYSTEM "relative.dtd" [`+strings.Repeat("%missing;",count)+`]><a/>`
  r,d,err:=parse(context.Background(),input,Options{Mode:mode},collect)
  if count==10000{
   if err!=nil||!r.valid||r.Original()!=input||r.RequestedMode()!=mode||r.EffectiveMode()!=Document||len(r.Diagnostics())!=0{t.Fatalf("count %d mode %v collect %t: result %+v err %v",count,mode,collect,r,err)}
   if collect{root:=dtdRoot(t,d);if len(root.children)!=0||len(root.attributes)!=0{t.Fatalf("missing PEs created nodes/defaults: %+v",root)}}else if d!=nil{t.Fatal("validation retained a tree")}
  }else{
   var pe *ParseError
   if !errors.As(err,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("count %d mode %v collect %t: result %+v tree %+v err %v",count,mode,collect,r,d,err)}
   primary:="invalid XML document";if mode==Content{primary="invalid XML content"}
   if pe.Category!=GrammarError||pe.RequestedMode!=mode||pe.EffectiveMode!=Document||pe.Error()!=primary||pe.Detail()!=detail||!reflect.DeepEqual(pe.Diagnostics,[]Diagnostic{diagnostic}){t.Fatalf("count %d mode %v collect %t: error %+v Detail %q diagnostics %+v",count,mode,collect,pe,pe.Detail(),pe.Diagnostics)}
  }
  accept(t,"<healthy/>",mode)
 }}}
}
