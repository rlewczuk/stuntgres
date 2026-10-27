package xml

import (
 "context"
 "errors"
 "fmt"
 "strings"
 "sync"
 "testing"
 "time"
 "unsafe"
)

// Capture948 independently exercises typed XML text/binary fields and xml_send,
// not the raw v::text cast. Metadata remains matching-source derived.
func TestXMLCoreOutputDeclarationEligibility(t *testing.T) {
 ctx:=context.Background()
 for _,label:=range []string{"UTF8","UTF-8","UTF16","UTF-16","utf8","utf-8","utf16","utf-16"} {
  for _,tail:=range []string{`standalone="yes"?>`,` standalone="yes"?>`,`?>`} {
   input:=`<?xml version="1.0" encoding="`+label+`"`+tail+`<a/>`
   for _,kind:=range []InputKind{Text,Binary} {
    t.Run(fmt.Sprintf("%s/%s/%d",label,tail,kind),func(t *testing.T){
     result,err:=Parse(ctx,input,Options{Mode:Document,InputKind:kind})
     if kind==Binary&&(label=="UTF16"||label=="UTF-16"||label=="utf16"||label=="utf-16") {
      var encoding *EncodingError
      if !errors.As(err,&encoding)||encoding.Kind!=InvalidEncodingName||encoding.Label!=label||encoding.SourceID!=-1||encoding.DestinationID!=6||encoding.DestinationName!="UTF8"||encoding.Error()!=fmt.Sprintf("invalid encoding name %q",label)||result.valid||result.original!=""||result.requested!=0||result.effective!=0||result.declaration!=(Declaration{})||result.diagnostics!=nil {t.Fatalf("binary label result%+v error%v",result,err)}
      if out,w,err:=OutputText(ctx,result);out!=""||w!=(OutputWarning{})||err==nil {t.Fatalf("invalid label text%q warning%+v error%v",out,w,err)}
      if out,w,err:=OutputBinary(ctx,result,8);out!=""||w!=(OutputWarning{})||err==nil {t.Fatalf("invalid label binary%q warning%+v error%v",out,w,err)}
      healthy:=accept(t,"<healthy/>",Document)
      if out,w,err:=OutputText(ctx,healthy);err!=nil||out!="<healthy/>"||w!=(OutputWarning{}) {t.Fatalf("invalid label reuse%q warning%+v error%v",out,w,err)}
      return
     }
     standalone:=Absent;if tail!=`?>` {standalone=Yes}
     declaration:=Declaration{Present:true,Version:"1.0",Encoding:label,EncodingPresent:true,Standalone:standalone,End:len(input)-4}
     if err!=nil||!result.valid||result.Original()!=input||result.RequestedMode()!=Document||result.EffectiveMode()!=Document||result.Declaration()!=declaration||len(result.Diagnostics())!=0 {t.Fatalf("accepted result%+v error%v",result,err)}
     adjacent:=tail==`standalone="yes"?>`
     warning:=OutputWarning{}
     text:="<a/>"
     if standalone==Yes {text=`<?xml version="1.0" standalone="yes"?><a/>`}
     if adjacent {text=input;warning=OutputWarning{Message:"could not parse XML declaration in stored value",Detail:"Space required."}}
     if out,w,err:=OutputText(ctx,result);err!=nil||out!=text||w!=warning {t.Fatalf("text%q warning%+v error%v want%q warning%+v",out,w,err,text,warning)}
     for _,target:=range []int{0,6,8} {
      want:=text
      if target==8&&!adjacent {
       want=`<?xml version="1.0" encoding="LATIN1"`
       if standalone==Yes {want+=` standalone="yes"`}
       want+=`?><a/>`
      }
      out,w,err:=OutputBinary(ctx,result,target)
      if err!=nil||out!=want||w!=warning {t.Fatalf("target%d output%q warning%+v error%v want%q warning%+v",target,out,w,err,want,warning)}
      if adjacent&&target!=8&&unsafe.StringData(out)!=unsafe.StringData(result.original) {t.Fatal("unchanged output copied original backing")}
      w.Message="caller mutation";w.Detail="caller mutation"
     }
     if result.Original()!=input||result.Declaration()!=declaration||len(result.Diagnostics())!=0 {t.Fatal("output mutated parse metadata")}
     healthy:=accept(t,"<healthy/>",Document)
     if out,w,err:=OutputText(ctx,healthy);err!=nil||out!="<healthy/>"||w!=(OutputWarning{}) {t.Fatalf("healthy output%q warning%+v error%v",out,w,err)}
    })
   }
  }
 }
 // The remaining independent spelling, admission and no-prefix controls from
 // capture948. The two Unicode body cases follow separately below.
 for _,tc:=range []struct{
  name,input,text,binary string
  version,encoding string
  standalone Standalone
  warning,versionWarning bool
  condition Condition
  message string
  offset int
 }{
  {name:"adjacent_no",input:`<?xml version="1.0" encoding="UTF8"standalone="no"?><a/>`,version:"1.0",encoding:"UTF8",standalone:No,warning:true},
  {name:"adjacent_single",input:`<?xml version='1.0' encoding='UTF8'standalone='yes'?><a/>`,version:"1.0",encoding:"UTF8",standalone:Yes,warning:true},
  {name:"adjacent_eq_spaces",input:"<?xml version=\"1.0\" encoding=\"UTF8\"standalone \t= \t\"yes\"?><a/>",version:"1.0",encoding:"UTF8",standalone:Yes,warning:true},
  {name:"adjacent_multiline",input:"<?xml version=\"1.0\"\nencoding=\"UTF8\"standalone=\"yes\"?><a/>",version:"1.0",encoding:"UTF8",standalone:Yes,warning:true},
  {name:"bom_immediate",input:"\ufeff<?xml version=\"1.0\" encoding=\"UTF8\"?><a/>",condition:"XML_ERR_DOCUMENT_EMPTY",message:"Start tag expected, '<' not found"},
  {name:"bom_adjacent",input:"\ufeff<?xml version=\"1.0\" encoding=\"UTF8\"standalone=\"yes\"?><a/>",condition:"XML_ERR_DOCUMENT_EMPTY",message:"Start tag expected, '<' not found"},
  {name:"bom_separated",input:"\ufeff<?xml version=\"1.0\" encoding=\"UTF8\" standalone=\"yes\"?><a/>",condition:"XML_ERR_DOCUMENT_EMPTY",message:"Start tag expected, '<' not found"},
  {name:"bom_plain",input:"\ufeff<a/>",condition:"XML_ERR_DOCUMENT_EMPTY",message:"Start tag expected, '<' not found"},
  {name:"plain",input:"<a/>",text:"<a/>",binary:`<?xml version="1.0" encoding="LATIN1"?><a/>`},
  {name:"one_lf",input:"\n<a/>",text:"<a/>",binary:"<?xml version=\"1.0\" encoding=\"LATIN1\"?>\n<a/>"},
  {name:"two_lf",input:"\n\n<a/>",text:"\n<a/>",binary:"<?xml version=\"1.0\" encoding=\"LATIN1\"?>\n\n<a/>"},
  {name:"legacy_separated",input:`<?xml version="1.0" encoding="LATIN1" standalone="yes"?><a/>`,version:"1.0",encoding:"LATIN1",standalone:Yes,text:`<?xml version="1.0" standalone="yes"?><a/>`,binary:`<?xml version="1.0" encoding="LATIN1" standalone="yes"?><a/>`},
  {name:"alias_separated",input:`<?xml version="1.0" encoding="UTF_8" standalone="yes"?><a/>`,version:"1.0",encoding:"UTF_8",standalone:Yes,text:`<?xml version="1.0" standalone="yes"?><a/>`,binary:`<?xml version="1.0" encoding="LATIN1" standalone="yes"?><a/>`},
  {name:"alias_adjacent",input:`<?xml version="1.0" encoding="UTF_8"standalone="yes"?><a/>`,version:"1.0",encoding:"UTF_8",standalone:Yes,condition:"XML_ERR_SPACE_REQUIRED",message:"Blank needed here",offset:36},
  {name:"version_adjacent",input:`<?xml version="1.0"standalone="yes"?><a/>`,version:"1.0",standalone:Yes,condition:"XML_ERR_SPACE_REQUIRED",message:"Blank needed here",offset:19},
  {name:"version_11_adjacent",input:`<?xml version="1.1" encoding="UTF8"standalone="yes"?><a/>`,version:"1.1",encoding:"UTF8",standalone:Yes,warning:true,versionWarning:true},
  {name:"legacy_adjacent",input:`<?xml version="1.0" encoding="LATIN1"standalone="yes"?><a/>`,version:"1.0",encoding:"LATIN1",standalone:Yes,condition:"XML_ERR_SPACE_REQUIRED",message:"Blank needed here",offset:37},
  {name:"special_junk",input:`<?xml version="1.0" encoding="UTF8"junk?><a/>`,version:"1.0",encoding:"UTF8",condition:"XML_ERR_XMLDECL_NOT_FINISHED",message:"parsing XML declaration: '?>' expected",offset:35},
 } {
  for _,kind:=range []InputKind{Text,Binary} {t.Run(fmt.Sprintf("%s/%d",tc.name,kind),func(t *testing.T){
   declaration:=Declaration{}
   if tc.version!="" {declaration=Declaration{Present:true,Version:tc.version,Encoding:tc.encoding,EncodingPresent:tc.encoding!="",Standalone:tc.standalone,End:len(tc.input)-4}}
   result,err:=Parse(ctx,tc.input,Options{Mode:Document,InputKind:kind})
   if tc.condition!="" {
    var failure *ParseError
    want:=Diagnostic{Condition:tc.condition,Domain:ParserDomain,Severity:Fatal,Line:1,Column:tc.offset+1,Offset:tc.offset,Message:tc.message,SourceContext:tc.input,Caret:strings.Repeat(" ",tc.offset)+"^"}
    detail:="line 1: "+tc.message+"\n"+tc.input+"\n"+strings.Repeat(" ",tc.offset)+"^"
    if !errors.As(err,&failure)||result.valid||result.original!=""||result.requested!=0||result.effective!=0||result.declaration!=(Declaration{})||result.diagnostics!=nil||failure.Category!=GrammarError||failure.RequestedMode!=Document||failure.EffectiveMode!=Document||failure.Declaration!=declaration||failure.SourceBaseOffset!=0||failure.Error()!="invalid XML document"||len(failure.Diagnostics)!=1||failure.Diagnostics[0]!=want||failure.Detail()!=detail {t.Fatalf("rejected result%+v error%+v want%+v detail%q",result,err,want,detail)}
    if out,w,err:=OutputText(ctx,result);out!=""||w!=(OutputWarning{})||err==nil {t.Fatalf("rejected output%q warning%+v error%v",out,w,err)}
    if out,w,err:=OutputBinary(ctx,result,8);out!=""||w!=(OutputWarning{})||err==nil {t.Fatalf("rejected binary%q warning%+v error%v",out,w,err)}
   }else{
    if err!=nil||!result.valid||result.Original()!=tc.input||result.RequestedMode()!=Document||result.EffectiveMode()!=Document||result.Declaration()!=declaration {t.Fatalf("accepted result%+v error%v want declaration%+v",result,err,declaration)}
    diagnostics:=result.Diagnostics()
    if tc.versionWarning {
     want:=Diagnostic{Condition:"XML_WAR_UNKNOWN_VERSION",Domain:ParserDomain,Severity:Warning,Line:1,Column:20,Offset:19,Message:"Unsupported version '1.1'",SourceContext:tc.input,Caret:strings.Repeat(" ",19)+"^"}
     if len(diagnostics)!=1||diagnostics[0]!=want {t.Fatalf("parse diagnostics%+v want%+v",diagnostics,want)}
    }else if len(diagnostics)!=0 {t.Fatalf("unexpected parse diagnostics%+v",diagnostics)}
    text,binary:=tc.text,tc.binary;warning:=OutputWarning{}
    if tc.warning {text,binary=tc.input,tc.input;warning=OutputWarning{Message:"could not parse XML declaration in stored value",Detail:"Space required."}}
    if out,w,err:=OutputText(ctx,result);err!=nil||out!=text||w!=warning {t.Fatalf("text%q warning%+v error%v want%q",out,w,err,text)}
    for _,target:=range []int{0,6,8} {want:=text;if target==8 {want=binary};if out,w,err:=OutputBinary(ctx,result,target);err!=nil||out!=want||w!=warning {t.Fatalf("target%d output%q warning%+v error%v want%q",target,out,w,err,want)}}
    if result.Original()!=tc.input||result.Declaration()!=declaration||len(result.Diagnostics())!=len(diagnostics) {t.Fatal("output mutated retained parse state")}
   }
   healthy:=accept(t,"<healthy/>",Document)
   if out,w,err:=OutputText(ctx,healthy);err!=nil||out!="<healthy/>"||w!=(OutputWarning{}) {t.Fatalf("reuse output%q warning%+v error%v",out,w,err)}
  })}
 }
 // Warning is committed before xml_send's target conversion. Capture948's
 // sigma witness reports it even when conversion subsequently fails.
 const prefix=`<?xml version="1.0" encoding="UTF8"standalone="yes"?>`
 warning:=OutputWarning{Message:"could not parse XML declaration in stored value",Detail:"Space required."}
 for _,body:=range []string{"é","∑"} {
  for _,kind:=range []InputKind{Text,Binary} {
  input:=prefix+"<a>"+body+"</a>"
  result,err:=Parse(ctx,input,Options{Mode:Document,InputKind:kind})
  declaration:=Declaration{Present:true,Version:"1.0",Encoding:"UTF8",EncodingPresent:true,Standalone:Yes,End:len(prefix)}
  if err!=nil||!result.valid||result.Original()!=input||result.RequestedMode()!=Document||result.EffectiveMode()!=Document||result.Declaration()!=declaration||len(result.Diagnostics())!=0 {t.Fatalf("body kind%d result%+v error%v",kind,result,err)}
  if out,w,err:=OutputText(ctx,result);err!=nil||out!=input||w!=warning {t.Fatalf("body text%q warning%+v error%v",out,w,err)}
  out,w,err:=OutputBinary(ctx,result,8)
  if body=="é" {
   if err!=nil||out!=prefix+"<a>\xe9</a>"||w!=warning {t.Fatalf("converted original%q warning%+v error%v",out,w,err)}
  }else{
   var encoding *EncodingError
   if out!=""||w!=warning||!errors.As(err,&encoding)||encoding.Kind!=UntranslatableCharacter||encoding.SourceID!=6||encoding.DestinationID!=8||encoding.Bytes!="0xe2 0x88 0x91"||encoding.Error()!=`character with byte sequence 0xe2 0x88 0x91 in encoding "UTF8" has no equivalent in encoding "LATIN1"` {t.Fatalf("conversion output%q warning%+v error%+v",out,w,err)}
  }
  for _,target:=range []int{0,6} {if out,w,err:=OutputBinary(ctx,result,target);err!=nil||out!=input||w!=warning {t.Fatalf("preserved output%q warning%+v error%v",out,w,err)}}
  if result.Original()!=input||len(result.Diagnostics())!=0 {t.Fatal("conversion changed retained value")}
  healthy:=accept(t,"<healthy/>",Document)
  if out,w,err:=OutputText(ctx,healthy);err!=nil||out!="<healthy/>"||w!=(OutputWarning{}) {t.Fatalf("body reuse%q warning%+v error%v",out,w,err)}
  }
 }
 // Capture957's CONTENT BOM controls are real accepted/rejected values, not
 // artificial DOCUMENT Results manufactured to exercise the output seam.
 for _,kind:=range []InputKind{Text,Binary} {
  input:="\ufeff<a/>"
  result,err:=Parse(ctx,input,Options{InputKind:kind})
  if err!=nil||!result.valid||result.Original()!=input||result.RequestedMode()!=Content||result.EffectiveMode()!=Content||result.Declaration()!=(Declaration{})||len(result.Diagnostics())!=0 {t.Fatalf("Content BOM result%+v error%v",result,err)}
  if out,w,err:=OutputText(ctx,result);err!=nil||out!=input||w!=(OutputWarning{}) {t.Fatalf("Content BOM text%q warning%+v error%v",out,w,err)}
  for _,target:=range []int{0,6} {if out,w,err:=OutputBinary(ctx,result,target);err!=nil||out!=input||w!=(OutputWarning{}) {t.Fatalf("Content BOM target%d output%q warning%+v error%v",target,out,w,err)}}
  out,w,err:=OutputBinary(ctx,result,8);var encoding *EncodingError
  if out!=""||w!=(OutputWarning{})||!errors.As(err,&encoding)||encoding.Kind!=UntranslatableCharacter||encoding.Bytes!="0xef 0xbb 0xbf"||encoding.Error()!=`character with byte sequence 0xef 0xbb 0xbf in encoding "UTF8" has no equivalent in encoding "LATIN1"` {t.Fatalf("Content BOM conversion%q warning%+v error%v",out,w,err)}
  input="\ufeff<?xml version=\"1.0\"?><a/>"
  result,err=Parse(ctx,input,Options{InputKind:kind});var failure *ParseError
  want:=Diagnostic{Condition:"XML_ERR_RESERVED_XML_NAME",Domain:ParserDomain,Severity:Fatal,Line:1,Column:7,Offset:8,Message:"XML declaration allowed only at the start of the document",SourceContext:input,Caret:"        ^"}
  if !errors.As(err,&failure)||result.valid||result.original!=""||result.requested!=0||result.effective!=0||result.declaration!=(Declaration{})||result.diagnostics!=nil||failure.Category!=GrammarError||failure.RequestedMode!=Content||failure.EffectiveMode!=Content||failure.Declaration!=(Declaration{})||failure.SourceBaseOffset!=0||failure.Error()!="invalid XML content"||len(failure.Diagnostics)!=1||failure.Diagnostics[0]!=want||failure.Detail()!="line 1: XML declaration allowed only at the start of the document\n"+input+"\n        ^" {t.Fatalf("Content BOM declaration result%+v error%+v want%+v",result,err,want)}
  if out,w,err:=OutputText(ctx,result);out!=""||w!=(OutputWarning{})||err==nil {t.Fatalf("Content rejected text%q warning%+v error%v",out,w,err)}
  if out,w,err:=OutputBinary(ctx,result,8);out!=""||w!=(OutputWarning{})||err==nil {t.Fatalf("Content rejected binary%q warning%+v error%v",out,w,err)}
  healthy:=accept(t,"<healthy/>",Content)
  if out,w,err:=OutputText(ctx,healthy);err!=nil||out!="<healthy/>"||w!=(OutputWarning{}) {t.Fatalf("Content reuse output%q warning%+v error%v",out,w,err)}
 }
}

func TestXMLCoreOutputWarningLifecycle(t *testing.T) {
 ctx:=context.Background()
 const prefix=`<?xml version="1.0" encoding="UTF8"standalone="yes"?>`
 input:=prefix+"<a>"+strings.Repeat("é",40000)+"</a>"
 retained:=accept(t,input,Document)
 warning:=OutputWarning{Message:"could not parse XML declaration in stored value",Detail:"Space required."}
 for _,tc:=range []struct{phase string;at int;final bool;want OutputWarning}{
  {"readContentDeclaration",1,false,OutputWarning{}},
  {"readContentDeclaration",0,true,warning},
  {"conversionPass",2,false,warning},
  {"conversionPass",4,false,warning},
  {"convertParts",0,true,warning},
 } {
  c:=&phaseContext{Context:ctx,phase:tc.phase,after:tc.at,final:tc.final,cause:context.Canceled}
  out,w,err:=OutputBinary(c,retained,8)
  if !c.admitted||out!=""||w!=tc.want||!errors.Is(err,context.Canceled) {t.Fatalf("phase%s/%d final%t admitted%t output%q warning%+v error%v",tc.phase,tc.at,tc.final,c.admitted,out,w,err)}
  if _,ok:=err.(interface{SQLState()string});ok {t.Fatal("ordinary cancellation acquired SQLSTATE")}
  if retained.Original()!=input||len(retained.Diagnostics())!=0 {t.Fatal("cancellation mutated retained Result")}
  healthy:=accept(t,"<healthy/>",Content)
  if out,w,err:=OutputText(ctx,healthy);err!=nil||out!="<healthy/>"||w!=(OutputWarning{}) {t.Fatalf("cancellation reuse%q warning%+v error%v",out,w,err)}
 }
 longPrefix:=`<?xml version="1.0"`+strings.Repeat(" ",40000)+`encoding="UTF8"standalone="yes"?><a/>`
 longValue:=accept(t,longPrefix,Document)
 for _,at:=range []int{2,4} {
  c:=&phaseContext{Context:ctx,phase:"readContentDeclaration",after:at,cause:context.Canceled}
  if out,w,err:=OutputText(c,longValue);!c.admitted||out!=""||w!=(OutputWarning{})||!errors.Is(err,context.Canceled) {t.Fatalf("prefix scan/%d admitted%t output%q warning%+v error%v",at,c.admitted,out,w,err)}
  if longValue.Original()!=longPrefix||len(longValue.Diagnostics())!=0 {t.Fatal("prefix cancellation changed retained value")}
  c=&phaseContext{Context:ctx,phase:"readContentDeclaration",after:at,cause:context.Canceled}
  if out,w,err:=OutputBinary(c,longValue,8);!c.admitted||out!=""||w!=(OutputWarning{})||!errors.Is(err,context.Canceled) {t.Fatalf("binary prefix scan/%d admitted%t output%q warning%+v error%v",at,c.admitted,out,w,err)}
  healthy:=accept(t,"<healthy/>",Content)
  if out,w,err:=OutputText(ctx,healthy);err!=nil||out!="<healthy/>"||w!=(OutputWarning{}) {t.Fatalf("prefix reuse%q warning%+v error%v",out,w,err)}
 }
 final:=&phaseContext{Context:ctx,phase:"readContentDeclaration",final:true,cause:context.DeadlineExceeded}
 if out,w,err:=OutputText(final,retained);!final.admitted||out!=""||w!=warning||!errors.Is(err,context.DeadlineExceeded) {t.Fatalf("no-copy final admitted%t output%q warning%+v error%v",final.admitted,out,w,err)}
 expired,cancelDeadline:=context.WithDeadline(ctx,time.Unix(1,0));defer cancelDeadline()
 if out,w,err:=OutputText(expired,retained);out!=""||w!=(OutputWarning{})||!errors.Is(err,context.DeadlineExceeded) {t.Fatalf("entry deadline output%q warning%+v error%v",out,w,err)}
 canceled,cancel:=context.WithCancel(ctx);cancel()
 if out,w,err:=OutputText(canceled,retained);out!=""||w!=(OutputWarning{})||!errors.Is(err,context.Canceled) {t.Fatalf("entry cancellation%q warning%+v error%v",out,w,err)}
 if out,w,err:=OutputText(ctx,Result{});out!=""||w!=(OutputWarning{})||err==nil {t.Fatalf("zero Result%q warning%+v error%v",out,w,err)}
 for _,target:=range []int{-1,42} {if out,w,err:=OutputBinary(ctx,retained,target);out!=""||w!=(OutputWarning{})||err==nil {t.Fatalf("invalid target%d output%q warning%+v error%v",target,out,w,err)}}
 if out,w,err:=OutputBinary(ctx,retained,7);out!=""||w!=warning {t.Fatalf("missing pair output%q warning%+v error%v",out,w,err)}else{
  var encoding *EncodingError
  if !errors.As(err,&encoding)||encoding.Kind!=MissingConversion||encoding.SourceID!=6||encoding.DestinationID!=7 {t.Fatalf("missing pair error%v",err)}
 }
 // Concurrent output has no warning sink or retained mutable operation state.
 var wg sync.WaitGroup
 failures:=make(chan error,8)
 for range 8 {wg.Add(1);go func(){defer wg.Done();for range 4 {
  out,w,err:=OutputText(ctx,retained)
  if err!=nil||out!=input||w!=warning {failures<-fmt.Errorf("concurrent output warning%+v error%v",w,err);return}
  w.Detail="changed"
  healthy,err:=Parse(ctx,"<healthy/>",Options{})
  if err!=nil {failures<-err;return}
  if out,w,err:=OutputText(ctx,healthy);err!=nil||out!="<healthy/>"||w!=(OutputWarning{}) {failures<-fmt.Errorf("healthy output%q warning%+v error%v",out,w,err);return}
 } }()}
 wg.Wait();close(failures);for err:=range failures {t.Error(err)}
 if retained.Original()!=input||len(retained.Diagnostics())!=0 {t.Fatal("retained ownership changed")}
}
