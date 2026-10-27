package xml

import (
 "context"
 "errors"
 "math"
 "strings"
 "testing"
)

func dtdAccountingLimits(t *testing.T) {
 t.Helper()
 dtdDefaultAndIdentifierLimits(t)
 dtdEmptyExpansionAccounting(t)
 TestXMLDTDGeneralEntityCopyVolume(t)
 // parser.c 141-275: every independent branch, strict equality, small-size
 // exemption, periodic active-input denominator and prior-loop short circuit.
 for _,tc:=range []struct{name string;size,count,checked,replacement uint64;consumed int;dtd bool;last Condition;ok bool}{
  {name:"small",size:999,ok:true},
  {name:"size_equal",size:1000,consumed:100},
  {name:"size_below",size:1000,consumed:101,ok:true},
  {name:"count_equal",size:1000,count:1000,consumed:300},
  {name:"count_below",size:1000,count:1000,consumed:301,ok:true},
  {name:"entity_equal",checked:20,consumed:3},
  {name:"entity_below",checked:20,consumed:4,ok:true},
  {name:"copy_small",replacement:9999999,ok:true},
  {name:"copy_equal",replacement:10000000,consumed:1000000},
  {name:"copy_below",replacement:10000000,consumed:1000001,ok:true},
  {name:"periodic_equal",count:10240,consumed:1024,dtd:true,ok:true},
  {name:"periodic_above",count:11264,consumed:1126,dtd:true},
  {name:"undeclared_at",count:10000,last:"XML_ERR_UNDECLARED_ENTITY",ok:true},
  {name:"undeclared_after",count:10001,last:"XML_ERR_UNDECLARED_ENTITY"},
  {name:"suppressed_at",count:10000,last:"XML_WAR_UNDECLARED_ENTITY",ok:true},
  {name:"suppressed_after",count:10001,last:"XML_WAR_UNDECLARED_ENTITY"},
 }{t.Run(tc.name,func(t *testing.T){
  p:=parser{ctx:context.Background(),s:strings.Repeat("x",tc.consumed),i:tc.consumed,line:1,column:tc.consumed+1,inDTD:tc.dtd,entityCount:tc.count,lastEntityError:tc.last}
  var e *entity;if tc.checked!=0{e=&entity{checked:tc.checked}}
  if got:=p.entityCheck(tc.size,e,tc.replacement);got!=tc.ok{t.Fatalf("got %v want %v",got,tc.ok)}
  if !tc.ok&&(len(p.events)!=1||p.events[0].Condition!="XML_ERR_ENTITY_LOOP"||p.events[0].Message!="Detected an entity reference loop"||p.events[0].Severity!=Fatal||p.events[0].Domain!=ParserDomain){t.Fatal(p.events)}
 })}
 p:=parser{ctx:context.Background(),s:"x",i:1,line:1,column:2,lastEntityError:"XML_ERR_ENTITY_LOOP"}
 if p.entityCheck(1,nil,0)||len(p.events)!=0{t.Fatal("prior loop did not short circuit")}
 p.lastEntityError="";p.entitySize=math.MaxUint64
 if p.entityCheck(1000,nil,0)||len(p.events)!=1{t.Fatal("consumed-size overflow")}
 p=parser{ctx:context.Background(),s:"x",i:1,line:1,column:2,inDTD:true,entityCount:11264,peFrames:[]inputFrame{{i:math.MaxInt},{i:math.MaxInt},{i:math.MaxInt}}}
 if p.entityCheck(0,nil,0)||len(p.events)!=1{t.Fatal("active-input overflow")}
 p=parser{ctx:context.Background(),s:"x",i:1,line:1,column:2,entityCopies:math.MaxUint64-5}
 p.countEntityCopy(&entity{value:"x"});if len(p.events)!=1||p.events[0].Condition!="XML_ERR_ENTITY_LOOP"{t.Fatal("copy-volume overflow")}
 for _,collect:=range []bool{false,true}{
  s:=`<!DOCTYPE a [<!ENTITY e "&e;">]><a x="&e;"/>`
  r,d,e:=parse(context.Background(),s,Options{Mode:Document},collect);var pe *ParseError
  if !errors.As(e,&pe)||r.valid||d!=nil{t.Fatal("string decoder recursion",r,e)}
  want:="line 1: Detected an entity reference loop\n"+s+"\n"+strings.Repeat(" ",len(s)-3)+"^"
  if pe.Detail()!=want{t.Fatal(pe.Detail())}
 }
 for _,tc:=range []struct{length,width int;ok bool}{{0,8,true},{math.MaxInt,16,false},{134217727,8,false},{134217726,8,true}} {
  p=parser{ctx:context.Background()}
  if p.collectionSize(tc.length,tc.width)!=tc.ok||!tc.ok&&p.err==nil{t.Fatal("checked collection storage",tc,p.err)}
 }
}

// Empty replacement trees take the release's reparse accounting, not the
// cached-node count or copy-volume path. This is a source-derived witness.
func dtdEmptyExpansionAccounting(t *testing.T) {
 t.Helper()
 for _,collect:=range []bool{false,true}{
  empty:=&entity{name:"empty"}
  outer:=&entity{name:"outer",value:"&empty;&empty;"}
  p:=parser{ctx:context.Background(),s:strings.Repeat(" ",100),i:20,line:1,column:21,effective:Content,dtdState:&dtdState{general:map[string]*entity{"empty":empty,"outer":outer}}}
  if collect{p.tree=&document{}}
  for invocation:=uint64(1);invocation<=3;invocation++{
   p.expandGeneral(outer)
   if p.err!=nil||p.failed(){t.Fatalf("collect %t invocation %d: %v %+v",collect,invocation,p.err,p.events)}
   if p.entityCount!=invocation*2||outer.childReferences!=2||outer.checked!=6||!outer.parsed||outer.shape!=0||p.entityCopies!=0{
    t.Fatalf("collect %t invocation %d: count=%d entity=%+v copy=%d",collect,invocation,p.entityCount,outer,p.entityCopies)
   }
   if collect&&len(p.tree.nodes)!=0{t.Fatalf("empty expansion made nodes: %+v",p.tree.nodes)}
  }
 }
}
