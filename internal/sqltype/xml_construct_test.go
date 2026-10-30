package sqltype

import (
 "context"
 "errors"
 "fmt"
 "strings"
 "testing"
 "unsafe"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/xml"
)

func TestXMLProduceSQLValueOwnership(t *testing.T){
 ctx:=context.Background();types,foreign:=testTypes(),testTypes();d:=types[XML]
 accepted,err:=XMLText(ctx,d,"<&\x01",false);if err!=nil||!accepted.ValidResult(d.Usage()){t.Fatal(accepted,err)}
 backing:=accepted.xml;characters:=unsafe.StringData(backing.result.Original());copied:=accepted
 if !SameDatum(accepted,copied)||copied.xml!=backing{t.Fatal("copy lost genuine generated backing")}
 retagged,err:=accepted.Retag(foreign[XML]);if err!=nil||retagged.xml!=backing{t.Fatal("retag",err)}
 domain,err:=Domain(99001,"xml_produce_domain","xml_produce_domain",d.Usage(),false);if err!=nil{t.Fatal(err)};wrapped,err:=domain.Wrap(accepted);if err!=nil||wrapped.xml!=backing{t.Fatal("domain",err)}
 equal,err:=accepted.SamePayload(ctx,retagged);if err!=nil||!equal{t.Fatal("payload",err)}
 bad,err:=XMLText(ctx,d,"bad--comment",true);if err==nil||bad.ValidResult(d.Usage()){t.Fatal("failed constructor published",bad,err)}
 output,err:=d.Output(ctx,accepted);if err!=nil||output!="&lt;&amp;\x01"{t.Fatal(output,err)}
 if accepted.xml!=backing||unsafe.StringData(backing.result.Original())!=characters||!SameDatum(accepted,copied){t.Fatal("subsequent operation changed accepted backing")}
 if bad,err:=xmlConstructed(d,xml.Result{},nil);err==nil||bad.validXML(){t.Fatal("zero provider result accepted")}
 canceled,cancel:=context.WithCancel(ctx);cancel();bad,err=XMLText(canceled,d,"healthy",false);if !errors.Is(err,context.Canceled)||bad.validXML(){t.Fatal("precancel",bad,err)}
}

func TestXMLProduceSQLMapping(t *testing.T){
 ctx:=context.Background();types:=testTypes();d:=types[XML]
 input,err:=XMLInput(ctx,d,"<?xml version=\"1.0\"?>\n<a/>",XMLContent,InputEnvironment{});if err!=nil{t.Fatal(err)}
 mapped,err:=XMLValueCharacters(ctx,input,false,OutputEnvironment{});if err!=nil||mapped!=`<a/>`{t.Fatal("typed XML output",mapped,err)}
 concat,err:=XMLConcat(ctx,d,[]Value{input});if err!=nil||concat.xml.result.Original()!="\n<a/>"{t.Fatal("raw concat body",concat,err)}
 bad,err:=XMLRoot(ctx,d,input,new(`x" y`),xml.RootPreserve);if err!=nil{t.Fatal(err)}
 var notices []sqlstate.Error
 mapped,err=XMLValueCharacters(ctx,bad,false,OutputEnvironment{XMLNotice:func(e sqlstate.Error){notices=append(notices,e)}});if err!=nil||mapped!="<?xml version=\"x\" y\"?>\n<a/>"||len(notices)!=1||notices[0].Code!="XX001"{t.Fatal(mapped,notices,err)}
 for _,n:=range []int{0,1,54,55,108,109}{raw:=strings.Repeat("\xff",n);got,err:=xmlBinaryCharacters(ctx,raw,false);if err!=nil{t.Fatal(err)};want:=strings.Repeat("/",n/3*4);if n%3==1{want+="/w=="};var lines []string;for len(want)>72{lines=append(lines,want[:72]);want=want[72:]};lines=append(lines,want);if got!=strings.Join(lines,"\r\n"){t.Fatalf("binary n%d %q",n,got)}}
 hex,err:=xmlBinaryCharacters(ctx,"\x00\xab\xcd\xef\x12\x34\xff",true);if err!=nil||hex!="00ABCDEF1234FF"{t.Fatal(hex,err)}
 binary,err:=Binary(types[Bytea],strings.Repeat("\xff",55));if err!=nil{t.Fatal(err)}
 mapped,err=XMLValueCharacters(ctx,binary,false,OutputEnvironment{})
 if err!=nil||mapped!=strings.Repeat("/",72)+"\r\n/w=="{t.Fatal("binary content was re-escaped",mapped,err)}
 mapped,err=XMLValueCharacters(ctx,binary,true,OutputEnvironment{})
 if err!=nil||mapped!=strings.Repeat("/",72)+"&#13;&#10;/w=="{t.Fatal("binary attribute was not escaped",mapped,err)}
 boolean:=Boolean(types[Bool],true);mapped,err=XMLValueCharacters(ctx,boolean,false,OutputEnvironment{});if err!=nil||mapped!="true"{t.Fatal(mapped,err)}
}

// Cancellation is triggered by actual mapping work, on a real cancel context.
// No sleep, public hook or workload-dependent timer selects the failing phase.
func TestXMLProduceSQLMappingCancellation(t *testing.T){
 types:=testTypes();d:=types[XML]
 for _,sparse:=range []bool{false,true}{
  elements:=make([]Value,4096)
  for i:=range elements{elements[i]=Null(types[Bool])}
  after,want:=5,"";if sparse{elements[0]=Boolean(types[Bool],true);after,want=16,"<element>true</element>"}
  array,err:=ArrayValue(types[1000],[]ArrayDimension{{Lower:1,Length:int32(len(elements))}},elements);if err!=nil{t.Fatal(err)}
  retained:=array;backing:=array.array;first:=&backing.elements[0]
  for _,run:=range []string{"characters","element","forest"}{
   t.Run(fmt.Sprintf("sparse=%t/%s",sparse,run),func(t *testing.T){
    underlying,cancel:=context.WithCancel(context.Background());defer cancel()
    ctx:=&arraySearchCancelContext{Context:underlying,cancel:cancel,remaining:after}
    var mapped string;var generated Value;var err error
    switch run{
    case "characters":mapped,err=XMLValueCharacters(ctx,array,false,OutputEnvironment{})
    case "element":generated,err=XMLElement(ctx,d,"r",nil,[]Value{array},OutputEnvironment{})
    case "forest":generated,err=XMLForest(ctx,d,[]string{"f"},[]Value{array},OutputEnvironment{})
    }
    if !errors.Is(err,context.Canceled)||!errors.Is(underlying.Err(),context.Canceled)||ctx.remaining>0||mapped!=""||generated.typ!=nil||generated.xml!=nil{t.Fatal("partial mapping publication",mapped,generated,err,ctx.remaining)}
    if !SameDatum(array,retained)||array.array!=backing||&backing.elements[0]!=first||len(backing.dimensions)!=1||backing.dimensions[0]!=(ArrayDimension{Lower:1,Length:int32(len(elements))}){t.Fatal("source array ownership changed")}
    for i,element:=range backing.elements{if !SameDatum(element,elements[i]){t.Fatal("source element changed",i)}}
    got,err:=XMLValueCharacters(context.Background(),array,false,OutputEnvironment{});if err!=nil||got!=want{t.Fatal("healthy mapping after cancellation",got,err)}
   })
  }
 }
 values:=make([]Value,4096);names:=make([]string,len(values))
 for i:=range values{values[i]=Null(d);names[i]="f"}
 for _,run:=range []string{"concat","element","forest"}{
  underlying,cancel:=context.WithCancel(context.Background())
  ctx:=&arraySearchCancelContext{Context:underlying,cancel:cancel,remaining:3}
  var got Value;var err error
  switch run{case "concat":got,err=XMLConcat(ctx,d,values);case "element":got,err=XMLElement(ctx,d,"r",nil,values,OutputEnvironment{});case "forest":got,err=XMLForest(ctx,d,names,values,OutputEnvironment{})}
  cancel()
  if !errors.Is(err,context.Canceled)||got.typ!=nil||got.xml!=nil{t.Fatal("NULL operand traversal",run,got,err)}
 }
}

func TestXMLProduceSQLMappingCompletionCancellation(t *testing.T){
 types:=testTypes();healthy:=context.Background()
 boolean,missing:=Boolean(types[Bool],true),Null(types[Bool])
 dimension:=ArrayDimension{Lower:3,Length:2}
 array,err:=ArrayValue(types[1000],[]ArrayDimension{dimension},[]Value{boolean,missing});if err!=nil{t.Fatal(err)}
 generated,err:=XMLText(healthy,types[XML],"<&",false);if err!=nil{t.Fatal(err)}
 binary,err:=Binary(types[Bytea],"\x00\xff");if err!=nil{t.Fatal(err)}
 for _,tc:=range []struct{name string;value Value;want string}{
  {"array",array,"<element>true</element>"},
  {"xml",generated,"&lt;&amp;"},
  {"bytea",binary,"AP8="},
 }{t.Run(tc.name,func(t *testing.T){
  source:=tc.value;xmlBacking,arrayBacking:=source.xml,source.array
  text,textBacking:=source.text,unsafe.StringData(source.text)
  var xmlCharacters *byte
  if xmlBacking!=nil{xmlCharacters=unsafe.StringData(xmlBacking.result.Original())}
  var arrayElements *Value;var arrayDimensions *ArrayDimension
  if arrayBacking!=nil{arrayElements=&arrayBacking.elements[0];arrayDimensions=&arrayBacking.dimensions[0]}
  unchanged:=func(){
   t.Helper()
   if !SameDatum(source,tc.value)||source.xml!=xmlBacking||source.array!=arrayBacking||source.text!=text||unsafe.StringData(source.text)!=textBacking{t.Fatal("mapping changed source backing")}
   if xmlBacking!=nil&&(xmlBacking.result.Original()!=tc.want||unsafe.StringData(xmlBacking.result.Original())!=xmlCharacters||xmlBacking.result.RequestedMode()!=XMLContent||xmlBacking.result.EffectiveMode()!=XMLContent){t.Fatal("mapping changed generated XML")}
   if arrayBacking!=nil&&(len(arrayBacking.dimensions)!=1||&arrayBacking.dimensions[0]!=arrayDimensions||arrayBacking.dimensions[0]!=dimension||len(arrayBacking.elements)!=2||&arrayBacking.elements[0]!=arrayElements||!SameDatum(arrayBacking.elements[0],boolean)||!SameDatum(arrayBacking.elements[1],missing)){t.Fatal("mapping changed source array")}
  }
  // Sweep every actual Err checkpoint, stopping only when mapping succeeds
  // without exhausting the counter. The immediately preceding invocation
  // cancels at the final checkpoint after a nonempty result was accumulated.
  for checkpoint:=1;;checkpoint++{
   underlying,cancel:=context.WithCancel(healthy)
   ctx:=&arraySearchCancelContext{Context:underlying,cancel:cancel,remaining:checkpoint}
   got,err:=XMLValueCharacters(ctx,source,false,OutputEnvironment{})
   effective:=underlying.Err();cancel()
   unchanged()
   replay,replayErr:=XMLValueCharacters(healthy,source,false,OutputEnvironment{});if replayErr!=nil||replay!=tc.want{t.Fatal("healthy reuse",replay,replayErr)}
   unchanged()
   if err==nil{
    if got!=tc.want||effective!=nil||ctx.remaining<=0||checkpoint<2{t.Fatal("checkpoint sweep did not complete",checkpoint,got,ctx.remaining,effective)}
    break
   }
   if !errors.Is(err,context.Canceled)||!errors.Is(effective,context.Canceled)||ctx.remaining>0||got!=""{t.Fatal("cancellation published mapped characters",checkpoint,got,err,ctx.remaining,effective)}
  }
 })}
}
