package xml

import (
 "context"
 "errors"
 "strings"
 "testing"
)

func TestXMLCoreQNameRecoveryDiagnostics(t *testing.T) {
 // Independent PostgreSQL18.6 UTF8/C captures513 precede these expectations.
 // Full Detail/context/caret are observed; conditions and character columns
 // follow matching Debian libxml2 productions, not SQL-exposed metadata.
 type event struct {
  condition Condition
  message string
  line,column,offset int
  source string
  caret int
 }
 type witness struct {
  name,input string
  events []event
  documentExtra bool
 }
 a:=strings.Repeat("a",50001)
 preview:=strings.Repeat("a",78)+"é"
 cases:=[]witness{
  {"initial_ncname_null","<"+a+"é:b/>",[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: NCName",1,50004,50004,preview,79},
  },false},
  {"second_colon_name_null","<a:b:"+a+"é/>",[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: Name",1,50008,50008,preview,79},
  },false},
  // The recovered initial name is :b, not the failed component plus :b.
  {"initial_recovered_spelling","<"+a+"é:b></b>",[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: NCName",1,50004,50004,preview,79},
   {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: :b line 1 and b",1,50011,50011,strings.Repeat("a",71)+"é:b></b>",79},
  },false},
  // Both successful components and the second colon survive the failed suffix.
  {"second_colon_recovered_spelling","<a:b:"+a+"é></a:b:>",[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: Name",1,50008,50008,preview,79},
  },false},
  {"initial_colon_name_still_null","<:"+a+"é/>",[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: Name",1,50005,50005,preview,79},
   {"XML_ERR_NAME_REQUIRED","StartTag: invalid element name",1,50005,50005,preview,79},
  },true},
  // Captures528: successful NmToken pieces are discontiguous after the failed
  // local component. The closing name proves that no failed payload survived.
  {"local_null_successful_token","<a:"+a+"é:c/>",[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: NCName",1,50006,50006,preview,79},
  },false},
  {"local_null_successful_token_spelling","<a:"+a+"é:c></a::c>",[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: NCName",1,50006,50006,preview,79},
  },false},
  {"initial_recovery_at_physical_end","<"+a+"é:",[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: NCName",1,50004,50004,preview,79},
   {"XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag : line 1",1,50005,50005,strings.Repeat("a",77)+"é:",79},
  },false},
  {"second_colon_recovery_at_physical_end","<a:b:"+a+"é",[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: Name",1,50008,50008,preview,79},
   {"XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag b: line 1",1,50008,50008,preview,79},
  },false},
  {"second_colon_ascii_rewind","<a:b:"+a+"/>",[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: Name",1,6,5,"<a:b:"+strings.Repeat("a",75),5},
   {"XML_ERR_NAME_TOO_LONG","Name too long: NCName",1,6,5,"<a:b:"+strings.Repeat("a",75),5},
   {"XML_ERR_NAME_REQUIRED","error parsing attribute name",1,6,5,"<a:b:"+strings.Repeat("a",75),5},
   {"XML_ERR_SPACE_REQUIRED","attributes construct error",1,6,5,"<a:b:"+strings.Repeat("a",75),5},
   {"XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag b: line 1",1,6,5,"<a:b:"+strings.Repeat("a",75),5},
  },true},
  {"comment_no_buffer","<a><!----x--></a>",[]event{
   {"XML_ERR_HYPHEN_IN_COMMENT","Double hyphen within comment",1,8,7,"<a><!----x--></a>",7},
  },false},
  {"comment_buffer_empty_display","<a><!--\r\n--x--></a>",[]event{
   {"XML_ERR_HYPHEN_IN_COMMENT","Double hyphen within comment: <!--",2,1,9,"--x--></a>",0},
  },false},
  // Positive committed hyphens allocate payload after the first empty event.
  {"comment_buffer_after_empty","<a><!------x--></a>",[]event{
   {"XML_ERR_HYPHEN_IN_COMMENT","Double hyphen within comment",1,8,7,"<a><!------x--></a>",7},
   {"XML_ERR_HYPHEN_IN_COMMENT","Double hyphen within comment: <!----",1,10,9,"<a><!------x--></a>",9},
  },false},
  {"comment_lf_buffer_empty_display","<a><!--\n--x--></a>",[]event{
   {"XML_ERR_HYPHEN_IN_COMMENT","Double hyphen within comment: <!--",2,1,8,"--x--></a>",0},
  },false},
 }
 for _,m:=range []Mode{Content,Document} {
  for _,tc:=range cases {
   t.Run(tc.name+map[Mode]string{Content:"/content",Document:"/document"}[m],func(t *testing.T){
    events:=append([]event(nil),tc.events...)
    if m==Document&&tc.documentExtra {
     last:=events[len(events)-1]
     last.condition="XML_ERR_DOCUMENT_END";last.message="Extra content at the end of the document"
     events=append(events,last)
    }
    details:=make([]string,len(events))
    for i,v:=range events {details[i]=blocks(v.line,v.source,v.caret,v.message)}
    wantDetail:=strings.Join(details,"\n")
    for _,kind:=range []InputKind{Text,Binary} {
     r,err:=Parse(context.Background(),tc.input,Options{Mode:m,InputKind:kind})
     var e *ParseError
     if !errors.As(err,&e)||r.valid||r.original!=""||r.requested!=0||r.effective!=0||r.declaration!=(Declaration{})||len(r.diagnostics)!=0 {t.Fatalf("kind%d result%+v error%v",kind,r,err)}
     primary:="invalid XML content";if m==Document {primary="invalid XML document"}
     if e.Error()!=primary||e.RequestedMode!=m||e.EffectiveMode!=m||e.Category!=GrammarError||e.SourceBaseOffset!=0||e.Declaration!=(Declaration{})||e.Detail()!=wantDetail||len(e.Diagnostics)!=len(events) {t.Fatalf("kind%d %+v detail%q want%q",kind,e,e.Detail(),wantDetail)}
     for i,v:=range events {
      want:=Diagnostic{Condition:v.condition,Domain:ParserDomain,Severity:Fatal,Line:v.line,Column:v.column,Offset:v.offset,Message:v.message,SourceContext:v.source,Caret:strings.Repeat(" ",v.caret)+"^"}
      if e.Diagnostics[i]!=want {t.Fatalf("kind%d event%d: %+v want %+v",kind,i,e.Diagnostics[i],want)}
     }
     healthy:="<healthy>é</healthy>"
     next,err:=Parse(context.Background(),healthy,Options{Mode:m,InputKind:kind})
     if err!=nil||!next.valid||next.Original()!=healthy||len(next.Diagnostics())!=0 {t.Fatalf("kind%d healthy reuse: %+v %v",kind,next,err)}
    }
   })
  }
 }
}

func TestXMLCoreNameReadNormalization(t *testing.T) {
 // Independent815/825/828 controls: 29 inputs, both modes/Text+Binary (116
 // primary combinations). Raw C3 contexts are captured split UTF8, not repaired
 // text. Structured offsets/columns follow the matching release productions.
 for _,eol:=range []struct{name,value string}{{"crlf","\r\n"},{"cr","\r"},{"lf","\n"}} {
  e:=eol.value;w:=len(e);h:=0;line:=2;col:=1
  if e=="\r\n" {h=1};if e=="\r" {line=1;col=50005}
  preview:=strings.Repeat("a",79)+"\xc3"
  nc:=currentReadWitness{name:"nc_overlength_"+eol.name,input:"<"+strings.Repeat("a",50001)+"é"+e+"/>",events:[]currentReadEvent{
   {"XML_ERR_NAME_TOO_LONG","Name too long: NCName",1,50004,50004+h,preview,79},
   {"XML_ERR_NAME_REQUIRED","StartTag: invalid element name",1,50004,50004+h,preview,79},
  },documentExtra:&currentReadEvent{"XML_ERR_DOCUMENT_END","Extra content at the end of the document",line,col,50004+w,"/>",0}}
  checkCurrentReadWitness(t,nc)
  if e=="\r" {col=50006}
  pn:=currentReadWitness{name:"pi_target_overlength_"+eol.name,input:"<?"+strings.Repeat("a",50001)+"é"+e+"?><a/>",events:[]currentReadEvent{
   {"XML_ERR_NAME_TOO_LONG","Name too long: Name",1,50005,50005+h,preview,79},
   {"XML_ERR_PI_NOT_STARTED","xmlParsePI : no target name",1,50005,50005+h,preview,79},
  },documentExtra:&currentReadEvent{"XML_ERR_DOCUMENT_EMPTY","Start tag expected, '<' not found",line,col,50005+w,"?><a/>",0}}
  checkCurrentReadWitness(t,pn)
  checkCurrentReadWitness(t,currentReadWitness{name:"nmtoken_overlength_"+eol.name,input:"<a:"+strings.Repeat("1",50001)+e+"/>",events:[]currentReadEvent{
   {"XML_ERR_NAME_TOO_LONG","Name too long: NmToken",1,50005,50004+h,strings.Repeat("1",80),79},
  }})
  for _,tc:=range []currentReadWitness{
   {name:"nc_endpoint_"+eol.name,input:"<é"+e+"/>"},
   {name:"pi_target_endpoint_"+eol.name,input:"<a><?gé"+e+"?></a>"},
   {name:"nmtoken_endpoint_"+eol.name,input:"<a:1"+e+"/>"},
  } {checkCurrentReadWitness(t,tc)}
 }
 for _,n:=range []int{49997,49998} {
  name:=strings.Repeat("a",n)+"é"
  checkCurrentReadWitness(t,currentReadWitness{name:"nc_boundary_"+map[int]string{49997:"below",49998:"exact"}[n],input:"<"+name+"\r\n/>"})
  checkCurrentReadWitness(t,currentReadWitness{name:"pi_target_boundary_"+map[int]string{49997:"below",49998:"exact"}[n],input:"<?"+name+"\r\n?><a/>"})
 }
 for _,n:=range []int{49999,50000} {
  checkCurrentReadWitness(t,currentReadWitness{name:"nmtoken_boundary_"+map[int]string{49999:"below",50000:"exact"}[n],input:"<a:"+strings.Repeat("1",n)+"\r\n/>"})
 }
 for _,tc:=range []currentReadWitness{
  {name:"ascii_nc_raw_terminator",input:"<"+strings.Repeat("a",50000)+"\r\n/>"},
  {name:"ascii_pi_target_raw_terminator",input:"<?"+strings.Repeat("a",50000)+"\r\n?><a/>"},
  {name:"prefixed_local_endpoint",input:"<p:é\r\n></p:é>"},
  {name:"fallback_token_endpoint",input:"<a:1\r\n></a:1>"},
  {name:"second_colon_endpoint",input:"<p:a:é\r\n></p:a:é>"},
 } {checkCurrentReadWitness(t,tc)}
}
