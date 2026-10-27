package xml

import (
 "context"
 "errors"
 "runtime"
 "strings"
 "sync"
 "testing"
 "time"
 "unsafe"
)

// This context admits a real private phase before cancellation. Stack inspection
// is test-only: production has neither hooks nor mutable cancellation telemetry.
type phaseContext struct {
 context.Context
 phase string
 after,seen int
 admitted bool
 cause error
 final bool
 canceled bool
}
func (c *phaseContext) Err() error {
 if c.canceled{return c.cause}
 var pcs [32]uintptr
 count:=runtime.Callers(2,pcs[:]);frames:=runtime.CallersFrames(pcs[:count]);inPhase:=false
 for {f,more:=frames.Next();if strings.HasSuffix(f.Function,"."+c.phase){inPhase=true};if !more{break}}
 if inPhase {c.admitted=true;c.seen++;if !c.final&&c.seen>=c.after{c.canceled=true;return c.cause}}
 if c.final&&c.admitted&&!inPhase {c.canceled=true;return c.cause}
 return nil
}
func TestXMLCoreCancellation(t *testing.T) {
 ctx,cancel:=context.WithCancel(context.Background());cancel()
 r,e:=Parse(ctx,"<a/>",Options{});if !errors.Is(e,context.Canceled)||r.valid{t.Fatal(r,e)}
 expired,cancelDeadline:=context.WithDeadline(context.Background(),time.Unix(1,0));defer cancelDeadline()
 r,e=Parse(expired,"<a/>",Options{});if !errors.Is(e,context.DeadlineExceeded)||r.valid{t.Fatal(r,e)}
 // Every source actually enters the named grammar production; the second and
 // later bounded polls are inside long data, not just API-entry cancellations.
 for _,tc:=range []struct{phase,input string;kind InputKind}{
  {"text","<a>"+strings.Repeat("x",40000)+"</a>",Text},
  {"scanName","<"+strings.Repeat("a",40000)+"/>",Text},
  {"scanName","<"+strings.Repeat("a",100000)+"é/>",Text},
  // End-name accelerators compare without moving the scanner until commit.
  // Admit both NameAndCompare and QNameAndCompare on their real long spans.
  {"endName","<"+strings.Repeat("a",40000)+"></"+strings.Repeat("a",40000)+">",Text},
  {"endName","<"+strings.Repeat("p",40000)+":"+strings.Repeat("a",40000)+"></"+strings.Repeat("p",40000)+":"+strings.Repeat("a",40000)+">",Text},
  {"comment","<a><!--"+strings.Repeat("x",40000)+"--></a>",Text},
  {"comment","<a><!--"+strings.Repeat("x-",40000)+"--></a>",Text},
  {"comment","<a><!--"+strings.Repeat("x\r\n",40000)+"--></a>",Text},
  {"commentComplex","<a><!--"+strings.Repeat("é",40000)+"--></a>",Text},
  {"commentComplex","<a><!--\r"+strings.Repeat("x",40000)+"--></a>",Text},
  {"pi","<a><?go "+strings.Repeat("x",40000)+"?></a>",Text},
  {"cdata","<a><![CDATA["+strings.Repeat("x",40000)+"]]></a>",Text},
  {"pi","<a><?go "+strings.Repeat("x\r\n",40000)+"?></a>",Text},
  {"cdata","<a><![CDATA["+strings.Repeat("x\r\n",40000)+"]]></a>",Text},
  {"attribute","<a x='"+strings.Repeat("x",40000)+"'/>",Text},
  // Namespace resolution performs real operation-only work after scanning.
  {"namespaceEqual",`<a xmlns:p="`+strings.Repeat("u",40000)+`" xmlns:q="`+strings.Repeat("u",40000)+`" p:x="1" q:x="2" q:x="3"/>`,Text},
  {"namespaceCopy",`<a xmlns:p="`+strings.Repeat("u",40000)+`&#117;"/>`,Text},
  // A skipped reference also reaches the lazy copy when a later literal
  // cannot extend the retained source span. Its grammar error must not mask
  // ordinary cancellation in that copy.
  {"namespaceCopy",`<a xmlns:p="`+strings.Repeat("u",40000)+`&#0;v"/>`,Text},
  {"attributeDuplicates",`<a a="1" b="2" c="3" d="4" e="5"/>`,Text},
  {"namespaceRestore",`<a xmlns:p="u" xmlns:q="v" xmlns:t="w" xmlns:s="x" xmlns:z="y"/>`,Text},
  {"attribute",`<a xmlns:p="`+strings.Repeat("u",40000)+`&#117;"/>`,Text},
  {"reference",`<a xmlns:p="&#`+strings.Repeat("0",40000)+`117;"/>`,Text},
  // Decoder failure does not turn later cancellation into a ParseError.
  {"text","<a>\xff"+strings.Repeat("x",40000)+"</b>",Binary},
  {"attribute","<a x='\xff"+strings.Repeat("x",40000)+"'></b>",Binary},
  {"pi","<a><?go \xff"+strings.Repeat("x",40000)+"?></b>",Binary},
  {"commentComplex","<a><!--\xff"+strings.Repeat("x",40000)+"--></b>",Binary},
  {"cdata","<a><![CDATA[\xff"+strings.Repeat("x",40000)+"]]></b>",Binary},
  {"pi","<a><?go \xff"+strings.Repeat("x\r\n",40000)+"?></b>",Binary},
  {"cdata","<a><![CDATA[\xff"+strings.Repeat("x\r\n",40000)+"]]></b>",Binary},
  // Rewound ASCII segments and PI limit returns reach cancellable CharData.
  {"text","<a><!--"+strings.Repeat("x",10000123)+"-->"+strings.Repeat("x",40000)+"</b>",Text},
  {"text","<a><?go "+strings.Repeat("x",10040001)+"?></b>",Text},
  // Failed local NCName payload is excluded from the recovered QName; only
  // the surviving prefix/token undergo checked, cancellable assembly.
  {"joinMessage","<"+strings.Repeat("p",40000)+":"+strings.Repeat("a",50001)+"é:"+strings.Repeat("c",40000)+"/>",Text},
  {"reference","<a>&#"+strings.Repeat("0",40000)+"65;</a>",Text},
  {"spaces","<a"+strings.Repeat(" ",40000)+"/>",Text},
  {"readContentDeclaration","<?xml version='"+strings.Repeat("x",40000)+"'?><a/>",Text},
  {"convertParts","<?xml version='1.0' encoding='LATIN1'?><a>"+strings.Repeat("\xe9",40000)+"</a>",Binary},
  {"conversionPass","<?xml version='1.0' encoding='LATIN1'?><a>"+strings.Repeat("\xe9",40000)+"</a>",Binary},
 } {
  positions:=[]int{2,4}
  if tc.phase=="conversionPass" {positions=append(positions,12,14)}
  if tc.phase=="scanName"&&len(tc.input)>100000 {positions=append(positions,16,20)}
  for _,at:=range positions {
   c:=&phaseContext{Context:context.Background(),phase:tc.phase,after:at,cause:context.Canceled}
   r,e=Parse(c,tc.input,Options{InputKind:tc.kind})
   if !c.admitted||!errors.Is(e,context.Canceled)||r.valid||r.Original()!="" {t.Fatalf("mid %s/%d admitted%v result%+v error%v",tc.phase,at,c.admitted,r,e)}
   if _,ok:=e.(interface{SQLState() string});ok{t.Fatal("cancellation acquired SQLSTATE")}
   accept(t,"<healthy/>",Content)
  }
 }
 for _,at:=range []int{2,4} {
  recovery:=&phaseContext{Context:context.Background(),phase:"finishDocumentDeclaration",after:at,cause:context.Canceled}
  r,e=Parse(recovery,`<?xml version='1.0' unknown='`+strings.Repeat("x",40000)+`'?><a/>`,Options{Mode:Document})
  if !recovery.admitted||!errors.Is(e,context.Canceled)||r.valid{t.Fatal("declaration recovery cancellation",r,e)}
  accept(t,"<healthy/>",Document)
 }
 c:=&phaseContext{Context:context.Background(),phase:"poll",final:true,cause:context.DeadlineExceeded}
 r,e=Parse(c,"<a>"+strings.Repeat("x",10000)+"</a>",Options{})
 if !c.admitted||!errors.Is(e,context.DeadlineExceeded)||r.valid {t.Fatal("prepublication",r,e)}
 retained:=accept(t,"<a>"+strings.Repeat("é",20000)+"</a>",Content)
 for _,target:=range []int{6,8} {
  out,w,e:=OutputBinary(ctx,retained,target);if out!=""||w!=(OutputWarning{})||!errors.Is(e,context.Canceled){t.Fatal(out,w,e)}
 }
 for _,at:=range []int{2,4,12,42,46} {
  c=&phaseContext{Context:context.Background(),phase:"conversionPass",after:at,cause:context.Canceled}
  out,w,e:=OutputBinary(c,retained,8)
  if !c.admitted||out!=""||w!=(OutputWarning{})||!errors.Is(e,context.Canceled){t.Fatalf("output mid %d: %q warning%+v error%v",at,out,w,e)}
 }
 // Normalization copies a real long body. The last position is its final
 // publication poll after all chunks, not a precanceled entry context.
 normalized:=accept(t,`<?xml version='1.1'?><a>`+strings.Repeat("x",40000)+`</a>`,Content)
 for _,at:=range []int{8,12,16,18} {
  c=&phaseContext{Context:context.Background(),phase:"output",after:at,cause:context.Canceled}
  out,w,err:=OutputText(c,normalized)
  if !c.admitted||out!=""||w!=(OutputWarning{})||!errors.Is(err,context.Canceled){t.Fatalf("normalization cancellation %d %q warning%+v error%v",at,out,w,err)}
  if _,ok:=err.(interface{SQLState() string});ok{t.Fatal("cancellation acquired SQLSTATE")}
 }
 if retained.Original()!="<a>"+strings.Repeat("é",20000)+"</a>" {t.Fatal("retained value changed")}
 accept(t,"<healthy/>",Content)
}
func TestXMLCoreOwnershipConcurrency(t *testing.T) {
 input:=`<?xml version='1.1'?><é>&amp;</é>`
 retained:=accept(t,input,Document)
 if unsafe.StringData(retained.original)!=unsafe.StringData(input){t.Fatal("immutable original was copied")}
 scopedInput:=`<r xmlns:p="u" xmlns:q="u"><b xmlns:q="v"/><a p:x="1" q:x="2" q:x="3"/></r>`
 scoped:=accept(t,scopedInput,Document)
 noHeader:=accept(t,"<owned>é</owned>",Content)
 noOp,w,err:=OutputText(context.Background(),noHeader)
 if err!=nil||w!=(OutputWarning{})||unsafe.StringData(noOp)!=unsafe.StringData(noHeader.original){t.Fatal("no-op output backing was copied")}
 warnings:=retained.Diagnostics();warnings[0].Message="mutated";warnings=append(warnings,Diagnostic{})
 if len(retained.Diagnostics())!=1||retained.Diagnostics()[0].Message!="Unsupported version '1.1'"{t.Fatal("borrowed diagnostics")}
 var wg sync.WaitGroup
 failures:=make(chan error,32)
 for worker:=range 32 {
  wg.Add(1);go func(){defer wg.Done()
   for iteration:=range 12 {
    mode:=Mode((worker+iteration)%2)
    r,e:=Parse(context.Background(),"<é x='&amp;'><b/></é>",Options{Mode:mode})
    if e!=nil||r.Original()!="<é x='&amp;'><b/></é>"||r.RequestedMode()!=mode {failures<-errors.New("independent parse");return}
    _,e=Parse(context.Background(),"<a>&bad;</b>",Options{Mode:mode});if e==nil{failures<-errors.New("missing independent failure");return}
    _,e=Parse(context.Background(),"<a>\xff\uFFFE</b>",Options{Mode:mode,InputKind:Binary});if e==nil{failures<-errors.New("missing decoder failure");return}
    r,e=Parse(context.Background(),"<é>é</é>",Options{Mode:mode});if e!=nil||len(r.Diagnostics())!=0{failures<-errors.New("leaked failed decoder state");return}
    for _,input:=range []string{
     `<a xmlns:p="u" xmlns:q="u" p:x="1" q:x="2" q:x="3"/>`,
     `<r xmlns:p="u" xmlns:q="u"><a p:x="1" q:x="2" q:x="3"/></r>`,
     scopedInput,
    } {
     parsed,err:=Parse(context.Background(),input,Options{Mode:mode})
     if err!=nil||!parsed.valid||parsed.Original()!=input||len(parsed.Diagnostics())!=0 {failures<-errors.New("concurrent scoped namespace acceptance");return}
    }
    parsed,err:=Parse(context.Background(),`<a p:x="1" q:x="2" q:x="3"/>`,Options{Mode:mode})
    var duplicate *ParseError
    if !errors.As(err,&duplicate)||parsed.valid||parsed.Original()!=""||len(duplicate.Diagnostics)!=1||duplicate.Diagnostics[0].Message!="Attribute q:x redefined" {failures<-errors.New("concurrent unbound lexical duplicate");return}
    out,w,err:=OutputText(context.Background(),scoped)
    if err!=nil||w!=(OutputWarning{})||out!=scopedInput||scoped.Original()!=scopedInput||len(scoped.Diagnostics())!=0 {failures<-errors.New("retained scoped namespace value");return}
    text,w,e:=OutputText(context.Background(),retained);if e!=nil||w!=(OutputWarning{})||text!=`<?xml version="1.1"?><é>&amp;</é>`{failures<-errors.New("retained output changed");return}
   }
  }()
 }
 wg.Wait();close(failures);for e:=range failures{t.Error(e)}
 if retained.Original()!=input||retained.Declaration().Version!="1.1"||len(retained.Diagnostics())!=1 {t.Fatal("retained ownership")}
 // The value has no parser/context/stack/attribute map: only strings, modes,
 // declaration, warning slice and validity are retained (asserted privately).
 if !retained.valid||retained.requested!=Document||retained.effective!=Document||retained.declaration.End!=21 {t.Fatal("private retained metadata")}
}
