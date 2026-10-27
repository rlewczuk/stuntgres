package xml

import (
 "context"
 "errors"
 "reflect"
 "strings"
 "testing"
)

func TestXMLDTDGeneralEntityCopyVolume(t *testing.T) {
 // Independently captured before the remedy: PostgreSQL18.6/180006 UTF8/C,
 // Debian libxml2 2.9.14+dfsg-1.3~deb12u6. Text10 accepts and expands to
 // exactly ten million x scalars; text11/12 have the SAME one-block failure
 // after reference11. Same-length markup10/11 was captured in DOCUMENT;
 // CONTENT markup follows the source-grounded effective-document route.
 literal:=strings.Repeat("x",1000000)
 markupText:=strings.Repeat("x",999993)
 expanded:=strings.Repeat("x",10000000)
 for _,tc:=range []struct{name,value,context string;counts []int}{
  {"text",literal,strings.Repeat("x",40)+`">]><a>`+strings.Repeat("&e;",11),[]int{10,11,12}},
  {"markup","<b>"+markupText+"</b>",strings.Repeat("x",36)+`</b>">]><a>`+strings.Repeat("&e;",11),[]int{10,11}},
 }{for _,count:=range tc.counts{for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
  input:=`<!DOCTYPE a [<!ENTITY e "`+tc.value+`">]><a>`+strings.Repeat("&e;",count)+`</a>`
  r,d,err:=parse(context.Background(),input,Options{Mode:mode},collect)
  if count==10{
   if err!=nil||!r.valid||r.Original()!=input||r.RequestedMode()!=mode||r.EffectiveMode()!=Document||len(r.Diagnostics())!=0{t.Fatalf("%s count %d mode %v collect %t: %+v %v",tc.name,count,mode,collect,r,err)}
   if collect{
    root:=dtdRoot(t,d)
    if tc.name=="text"{
     if len(root.children)!=1||root.children[0].kind!=textNode||root.children[0].value!=expanded{t.Fatalf("text expansion: children %d",len(root.children))}
    }else{
     if len(root.children)!=10{t.Fatalf("markup children %d",len(root.children))}
     for i,b:=range root.children{if b.kind!=elementNode||b.name!="b"||len(b.children)!=1||b.children[0].kind!=textNode||b.children[0].value!=markupText||i>0&&b==root.children[i-1]{t.Fatalf("markup child %d: %+v",i,b)}}
    }
   }else if d!=nil{t.Fatal("validation retained a tree")}
  }else{
   var pe *ParseError
   if !errors.As(err,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("%s count %d mode %v collect %t: %+v tree %+v err %v",tc.name,count,mode,collect,r,d,err)}
   caret:=strings.Repeat(" ",79)+"^"
   detail:="line 1: Detected an entity reference loop\n"+tc.context+"\n"+caret
   // Complete rendered blocks are captured; structured source coordinates
   // follow the pinned original-input cursor, not candidate observations.
   diagnostic:=Diagnostic{Condition:"XML_ERR_ENTITY_LOOP",Domain:ParserDomain,Severity:Fatal,Line:1,Column:1000066,Offset:1000065,SourceBaseOffset:0,Message:"Detected an entity reference loop",SourceContext:tc.context,Caret:caret,Source:OriginalSource}
   primary:="invalid XML document";if mode==Content{primary="invalid XML content"}
   if pe.Category!=GrammarError||pe.RequestedMode!=mode||pe.EffectiveMode!=Document||pe.Error()!=primary||pe.Detail()!=detail||!reflect.DeepEqual(pe.Diagnostics,[]Diagnostic{diagnostic}){t.Fatalf("%s count %d mode %v collect %t: %+v Detail %q diagnostics %+v",tc.name,count,mode,collect,pe,pe.Detail(),pe.Diagnostics)}
  }
  accept(t,"<healthy/>",mode)
 }}}}
 dtdFirstCopyOwnership(t)
}

func dtdFirstCopyOwnership(t *testing.T) {
 t.Helper()
 // Source-derived parser.c7229-7430: text owner1/listNULL and nontext
 // owner0/listnonnull both charge first publication. Document and balanced
 // contexts each have a real input. Childless lists return before any charge.
 for _,tc:=range []struct{name,value string;copy,owner uint64;shape uint8}{
  {"empty","",0,0,0},
  {"text","ok",7,1,1},
  {"markup","<b/>",9,0,2},
  {"mixed","x<b/>y",11,0,2},
  {"empty CDATA","<![CDATA[]]>",17,0,2},
  {"comment","<!--x-->",13,0,2},
  {"PI","<?go?>",11,0,2},
 }{for _,depth:=range []int{0,2}{for _,collect:=range []bool{false,true}{
  e:=&entity{name:"e",value:tc.value}
  p:=parser{ctx:context.Background(),s:strings.Repeat(" ",100),i:100,line:1,column:101,effective:Content,entityDepth:depth,dtdState:&dtdState{general:map[string]*entity{"e":e}}}
  if collect{p.tree=&document{}}
  for invocation:=uint64(1);invocation<=2;invocation++{
   p.expandGeneral(e)
   if p.err!=nil||p.failed()||p.entityCopies!=tc.copy*invocation||e.owner!=tc.owner||e.shape!=tc.shape{t.Fatalf("%s depth %d collect %t invocation %d: copy %d owner %d shape %d err %v events %+v",tc.name,depth,collect,invocation,p.entityCopies,e.owner,e.shape,p.err,p.events)}
   if collect&&tc.shape==0&&len(p.tree.nodes)!=0{t.Fatal("childless replacement published nodes")}
  }
 }}}
}

func TestXMLDTDGeneralEntityCopyCancellation(t *testing.T) {
 input:=`<!DOCTYPE a [<!ENTITY e "<b>ok</b>">]><a>&e;&e;</a>`
 for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{for _,cause:=range []error{context.Canceled,context.DeadlineExceeded}{for _,at:=range []int{1,2}{
  // The first poll is first-publication accounting; the second is the
  // cached-publication path. Both are genuine noncursor counter work.
  c:=&phaseContext{Context:context.Background(),phase:"countEntityCopy",after:at,cause:cause}
  r,d,err:=parse(c,input,Options{Mode:mode},collect)
  if !c.admitted||c.seen<at||!errors.Is(err,cause)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("mode %v collect %t poll %d admitted %t seen %d: %+v tree %+v err %v",mode,collect,at,c.admitted,c.seen,r,d,err)}
  if _,ok:=err.(interface{SQLState()string});ok{t.Fatal("context error acquired SQLSTATE")}
  accept(t,"<healthy/>",mode)
 }}}}
}
