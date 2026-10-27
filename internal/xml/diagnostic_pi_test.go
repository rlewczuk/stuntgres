package xml

import (
 "context"
 "errors"
 "fmt"
 "strings"
 "testing"
)

// Independent PostgreSQL18.6 UTF8/C captures815/825/828 are mapped losslessly in
// local://xml-core-pi-crlf-evidence.md. Messages, contexts, carets and acceptance
// are observed; conditions, domains, severity, offsets and character columns
// follow matching Debian libxml2 sources, not SQL-exposed diagnostic metadata.
type currentReadEvent struct {
 condition Condition
 message string
 line,column,offset int
 source string
 caret int
}
type currentReadWitness struct {
 name,input string
 events []currentReadEvent
 binaryOnly bool
 documentExtra *currentReadEvent
 contentExtra *currentReadEvent
}

// All three reporting-read families exercise the real parser and publication
// contract. Expected blocks come only from literal captured/source-mapped facts,
// never the candidate's Diagnostic.String or context renderer.
func checkCurrentReadWitness(t *testing.T,tc currentReadWitness) {
 t.Helper()
 for _,mode:=range []Mode{Content,Document} {
  t.Run(tc.name+map[Mode]string{Content:"/content",Document:"/document"}[mode],func(t *testing.T){
   events:=tc.events
   if mode==Document&&tc.documentExtra!=nil {events=append(append([]currentReadEvent(nil),events...),*tc.documentExtra)}
   if mode==Content&&tc.contentExtra!=nil {events=append(append([]currentReadEvent(nil),events...),*tc.contentExtra)}
   kinds:=[]InputKind{Text,Binary};if tc.binaryOnly {kinds=[]InputKind{Binary}}
   for _,kind:=range kinds {
    ctx:=context.Background()
    result,err:=Parse(ctx,tc.input,Options{Mode:mode,InputKind:kind})
    if len(events)==0 {
     if err!=nil||!result.valid||result.Original()!=tc.input||result.RequestedMode()!=mode||result.EffectiveMode()!=mode||result.Declaration()!=(Declaration{})||len(result.Diagnostics())!=0 {t.Fatalf("kind%d accepted valid%t error%v",kind,result.valid,err)}
     if out,w,err:=OutputText(ctx,result);err!=nil||out!=tc.input||w!=(OutputWarning{}) {t.Fatalf("kind%d accepted text length%d error%v",kind,len(out),err)}
     if out,w,err:=OutputBinary(ctx,result,6);err!=nil||out!=tc.input||w!=(OutputWarning{}) {t.Fatalf("kind%d accepted binary length%d error%v",kind,len(out),err)}
    }else{
     var failure *ParseError
     if !errors.As(err,&failure)||result.valid||result.original!=""||result.requested!=0||result.effective!=0||result.declaration!=(Declaration{})||result.diagnostics!=nil {t.Fatalf("kind%d failure valid%t error%v",kind,result.valid,err)}
     if len(failure.Diagnostics)!=len(events) {t.Fatalf("kind%d diagnostics%+v want%+v",kind,failure.Diagnostics,events)}
     detail:=make([]string,len(events))
     for i,v:=range events {
      caret:=strings.Repeat(" ",v.caret)+"^"
      want:=Diagnostic{Condition:v.condition,Domain:ParserDomain,Severity:Fatal,Line:v.line,Column:v.column,Offset:v.offset,Message:v.message,SourceContext:v.source,Caret:caret}
      if failure.Diagnostics[i]!=want {t.Fatalf("kind%d event%d %+v want%+v",kind,i,failure.Diagnostics[i],want)}
      detail[i]=fmt.Sprintf("line %d: %s\n%s\n%s",v.line,v.message,v.source,caret)
     }
     primary:="invalid XML content";if mode==Document {primary="invalid XML document"}
     if failure.Error()!=primary||failure.RequestedMode!=mode||failure.EffectiveMode!=mode||failure.Category!=GrammarError||failure.SourceBaseOffset!=0||failure.Declaration!=(Declaration{})||failure.Detail()!=strings.Join(detail,"\n") {t.Fatalf("kind%d error%+v detail%q want%q",kind,failure,failure.Detail(),strings.Join(detail,"\n"))}
     if out,w,err:=OutputText(ctx,result);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("kind%d failed text%q error%v",kind,out,err)}
     if out,w,err:=OutputBinary(ctx,result,6);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("kind%d failed binary%q error%v",kind,out,err)}
    }
    healthy:="<healthy>é</healthy>"
    next,err:=Parse(ctx,healthy,Options{Mode:mode,InputKind:kind})
    if err!=nil||!next.valid||next.Original()!=healthy||next.RequestedMode()!=mode||next.EffectiveMode()!=mode||next.Declaration()!=(Declaration{})||len(next.Diagnostics())!=0 {t.Fatalf("kind%d healthy reuse valid%t error%v",kind,next.valid,err)}
    if out,w,err:=OutputText(ctx,next);err!=nil||out!=healthy||w!=(OutputWarning{}) {t.Fatalf("kind%d healthy text%q error%v",kind,out,err)}
    if out,w,err:=OutputBinary(ctx,next,6);err!=nil||out!=healthy||w!=(OutputWarning{}) {t.Fatalf("kind%d healthy binary%q error%v",kind,out,err)}
   }
  })
 }
}

func TestXMLCorePICRLFRead(t *testing.T) {
 // 24 validUTF8 witnesses in both modes/Text+Binary, six genuine raw binary
 // witnesses in both modes/Binary only: 108 primary Parse combinations.
 const mismatch="Opening and ending tag mismatch: a line 1 and b"
 for _,eol:=range []struct{name,value string}{{"crlf","\r\n"},{"cr","\r"},{"lf","\n"}} {
  e:=eol.value;w:=len(e);h:=0;line:=2;if e=="\r\n" {h=1};if e=="\r" {line=1}
  for _,n:=range []int{9999999,10000000,10000001} {
   tc:=currentReadWitness{name:fmt.Sprintf("threshold_%d_%s",n,eol.name),input:"<a><?go "+strings.Repeat("x",n)+e+"?></a>"}
   q:=8+n
   if n==10000000 {
    col:=1;if e=="\r" {col=q+2}
    tc.events=[]currentReadEvent{{"XML_ERR_PI_NOT_FINISHED","PI go too big found",line,col,q+w,"?></a>",0}}
   }else if n==10000001 {
    tc.events=[]currentReadEvent{{"XML_ERR_PI_NOT_FINISHED","PI go too big found",1,q+1,q+h,strings.Repeat("x",80),79}}
   }
   checkCurrentReadWitness(t,tc)
  }
  for _,tail:=range []struct{name,value string}{{"plain","z"},{"literal_entity","&bad;"},{"range","\uFFFE"}} {
   q:=10000006;col:=1;if e=="\r" {col=q+2}
   source:=tail.value+"?></b>"
   tc:=currentReadWitness{name:"cache_"+eol.name+"_"+tail.name,input:"<a><?go "+strings.Repeat("x",9999998)+e+source}
   switch tail.name {
   case "plain":tc.events=[]currentReadEvent{{"XML_ERR_TAG_NAME_MISMATCH",mismatch,line,col+7,q+w+7,source,7}}
   case "literal_entity":tc.events=[]currentReadEvent{
    {"XML_ERR_PI_NOT_FINISHED","PI go too big found",line,col+2,q+w+2,source,2},
    {"XML_ERR_TAG_NAME_MISMATCH",mismatch,line,col+11,q+w+11,source,11},
   }
   case "range":tc.events=[]currentReadEvent{
    {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",line,col,q+w,source,0},
    {"XML_ERR_PI_NOT_FINISHED","ParsePI: PI go never end ...",line,col,q+w,source,0},
    {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",line,col,q+w,source,0},
    {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 65534",line,col,q+w,source,0},
    {"XML_ERR_TAG_NAME_MISMATCH",mismatch,line,col+7,q+w+9,source,9},
   }
   }
   checkCurrentReadWitness(t,tc)
  }
  for _,payload:=range []string{"x","é"} {
   col:=8;if e=="\r" {col=18}
   checkCurrentReadWitness(t,currentReadWitness{name:"small_"+eol.name+"_"+payload,input:"<a><?go "+payload+e+"z?></b>",events:[]currentReadEvent{{"XML_ERR_TAG_NAME_MISMATCH",mismatch,line,col,8+len(payload)+w+7,"z?></b>",7}}})
  }
  for _,tail:=range []struct{name,value,preview string}{
   {"plain","x",map[string]string{"crlf":"0xFF 0x0D 0x0A 0x78","cr":"0xFF 0x0D 0x78 0x3F","lf":"0xFF 0x0A 0x78 0x3F"}[eol.name]},
   {"range_bytes","\xef\xbf\xbe",map[string]string{"crlf":"0xFF 0x0D 0x0A 0xEF","cr":"0xFF 0x0D 0xEF 0xBF","lf":"0xFF 0x0A 0xEF 0xBF"}[eol.name]},
  } {
   col:=1+len(tail.value)+6;if e=="\r" {col=11+len(tail.value)+6}
   checkCurrentReadWitness(t,currentReadWitness{name:"binary_"+eol.name+"_"+tail.name,input:"<a><?go \xff"+e+tail.value+"?></b>",binaryOnly:true,events:[]currentReadEvent{
    {"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: "+tail.preview,1,9,8,"<a><?go \xff",8},
    {"XML_ERR_TAG_NAME_MISMATCH",mismatch,line,col,9+w+len(tail.value)+6,tail.value+"?></b>",len(tail.value)+6},
   }})
  }
 }
}
