package xml

import (
 "context"
 "errors"
 "fmt"
 "strings"
 "testing"
)

// Independent PostgreSQL18.6 UTF8/C text and genuine binary-receive captures908/922
// ground acceptance and complete English Detail/context/caret. Conditions,
// domains, severity, character columns and declaration metadata follow matching
// Debian libxml2/XML provider source, not SQL-exposed libxml metadata.
func TestXMLCoreDeclarationEncodingState(t *testing.T) {
 type event struct {condition Condition;message string;offset int}
 type witness struct {
  name,input string
  document,content Declaration
  events []event
  contentDetail string
  binaryInvalid bool
  binaryLabel string
  tabCaret bool
 }
 const completion="parsing XML declaration: '?>' expected"
 const customCompletion="Parsing XML declaration: '?>' expected."
 const missingEncoding="Missing encoding in text declaration."
 const unclosed="String not closed expecting \" or '"
 const root="Start tag expected, '<' not found"
 blank:=func(at int)event{return event{"XML_ERR_SPACE_REQUIRED","Blank needed here",at}}
 finish:=func(at int)event{return event{"XML_ERR_XMLDECL_NOT_FINISHED",completion,at}}
 closed:=func(label string,end int,standalone Standalone)Declaration{return Declaration{Present:true,Version:"1.0",Encoding:label,EncodingPresent:true,Standalone:standalone,End:end}}
 absent:=Declaration{Present:true,Version:"1.0"}
 var cases []witness
 // Five independently captured suffixes for each distinct assignment branch.
 for _,label:=range []string{"LATIN1","UTF8","UTF-8","UTF16","UTF-16"} {
  prefix:=`<?xml version="1.0" encoding="`+label+`"`
  for i,tail:=range []string{`junk?><a/>`,` junk?><a/>`,`?><a/>`,`standalone="yes"?><a/>`,` standalone="yes"?><a/>`} {
   input:=prefix+tail
   end:=len(input)-4
   tc:=witness{name:fmt.Sprintf("%s_%d",label,i),input:input,document:closed(label,end,Absent),content:closed(label,0,Absent),binaryInvalid:label=="UTF16"||label=="UTF-16",binaryLabel:label}
   switch i {
   case 0:
    if label=="LATIN1" {tc.events=append(tc.events,blank(len(prefix)))}
    tc.events=append(tc.events,finish(len(prefix)));tc.contentDetail=customCompletion
   case 1:tc.events=[]event{finish(len(prefix)+1)};tc.contentDetail=customCompletion
   case 2:tc.content.End=end
   case 3:
    tc.document.Standalone=Yes;tc.contentDetail="Space required."
    if label=="LATIN1" {tc.events=[]event{blank(len(prefix))}}
   case 4:tc.document.Standalone=Yes;tc.content.Standalone=Yes;tc.content.End=end
   }
   cases=append(cases,tc)
  }
 }
 cases=append(cases,
  witness{name:"no_encoding_unknown",input:`<?xml version="1.0" junk?><a/>`,document:Declaration{Present:true,Version:"1.0",End:26},content:absent,contentDetail:customCompletion,events:[]event{finish(20)}},
  witness{name:"legacy_tab",input:"<?xml version=\"1.0\" encoding=\"LATIN1\"\tjunk?><a/>",document:closed("LATIN1",44,Absent),content:closed("LATIN1",0,Absent),contentDetail:customCompletion,events:[]event{finish(38)},tabCaret:true},
  witness{name:"legacy_gt",input:`<?xml version="1.0" encoding="LATIN1"><a/>`,document:closed("LATIN1",38,Absent),content:closed("LATIN1",0,Absent),contentDetail:customCompletion,events:[]event{blank(37),finish(37)}},
  witness{name:"legacy_eof",input:`<?xml version="1.0" encoding="LATIN1"`,document:closed("LATIN1",0,Absent),content:closed("LATIN1",0,Absent),contentDetail:customCompletion,events:[]event{blank(37),finish(37),{"XML_ERR_DOCUMENT_EMPTY",root,37}}},
  witness{name:"unclosed_legacy",input:`<?xml version="1.0" encoding="LATIN1junk?><a/>`,document:Declaration{Present:true,Version:"1.0",End:42},content:absent,contentDetail:missingEncoding,events:[]event{{"XML_ERR_STRING_NOT_CLOSED",unclosed,40}}},
  witness{name:"unclosed_adjacent",input:`<?xml version="1.0" encoding="LATIN1 standalone='yes'?><a/>`,document:Declaration{Present:true,Version:"1.0",Standalone:Yes,End:55},content:absent,contentDetail:missingEncoding,events:[]event{{"XML_ERR_STRING_NOT_CLOSED",unclosed,36}}},
  witness{name:"unquoted",input:`<?xml version="1.0" encoding=LATIN1junk?><a/>`,document:Declaration{Present:true,Version:"1.0",End:41},content:absent,contentDetail:missingEncoding,events:[]event{{"XML_ERR_STRING_NOT_STARTED","String not started expecting ' or \"",29},finish(29)}},
  witness{name:"missing_equal",input:`<?xml version="1.0" encoding"LATIN1"junk?><a/>`,document:Declaration{Present:true,Version:"1.0",End:42},content:absent,contentDetail:missingEncoding,events:[]event{{"XML_ERR_EQUAL_REQUIRED","expected '='",28},finish(28)}},
  witness{name:"empty",input:`<?xml version="1.0" encoding=""junk?><a/>`,document:closed("",37,Absent),content:closed("",0,Absent),contentDetail:customCompletion,binaryInvalid:true,events:[]event{{"XML_ERR_ENCODING_NAME","Invalid XML encoding name",30},finish(31)}},
  witness{name:"digit",input:`<?xml version="1.0" encoding="1X"junk?><a/>`,document:Declaration{Present:true,Version:"1.0",End:39},content:closed("1X",0,Absent),contentDetail:customCompletion,binaryInvalid:true,binaryLabel:"1X",events:[]event{{"XML_ERR_ENCODING_NAME","Invalid XML encoding name",30},{"XML_ERR_STRING_NOT_CLOSED",unclosed,30},finish(30)}},
  witness{name:"invalid_suffix",input:`<?xml version="1.0" encoding="LATIN1!"junk?><a/>`,document:Declaration{Present:true,Version:"1.0",End:44},content:closed("LATIN1!",0,Absent),contentDetail:customCompletion,events:[]event{{"XML_ERR_STRING_NOT_CLOSED",unclosed,36},finish(36)}},
  witness{name:"empty_eof",input:`<?xml version="1.0" encoding="`,document:absent,content:absent,contentDetail:missingEncoding,events:[]event{{"XML_ERR_ENCODING_NAME","Invalid XML encoding name",30},{"XML_ERR_STRING_NOT_CLOSED",unclosed,30},finish(30),{"XML_ERR_DOCUMENT_EMPTY",root,30}}},
  witness{name:"legacy_eof_space",input:`<?xml version="1.0" encoding="LATIN1" `,document:closed("LATIN1",0,Absent),content:closed("LATIN1",0,Absent),contentDetail:customCompletion,events:[]event{finish(38),{"XML_ERR_DOCUMENT_EMPTY",root,38}}},
  witness{name:"standalone_no_equal",input:`<?xml version="1.0" encoding="LATIN1"standalone"yes"?><a/>`,document:closed("LATIN1",54,Absent),content:closed("LATIN1",0,Absent),contentDetail:"Space required.",events:[]event{blank(37),{"XML_ERR_EQUAL_REQUIRED","expected '='",47},finish(47)}},
  witness{name:"standalone_no_quote",input:`<?xml version="1.0" encoding="LATIN1"standalone=yes?><a/>`,document:closed("LATIN1",53,Absent),content:closed("LATIN1",0,Absent),contentDetail:"Space required.",events:[]event{blank(37),{"XML_ERR_STRING_NOT_STARTED","String not started expecting ' or \"",48},finish(48)}},
  witness{name:"version_adjacent",input:`<?xml version="1.0"encoding="LATIN1"junk?><a/>`,document:closed("LATIN1",42,Absent),content:absent,contentDetail:"Space required.",events:[]event{blank(19),blank(36),finish(36)}},
  witness{name:"version_missing",input:`<?xml encoding="LATIN1"junk?><a/>`,document:Declaration{Present:true,Encoding:"LATIN1",EncodingPresent:true,End:29},content:Declaration{Present:true},contentDetail:"Malformed declaration: missing version.",events:[]event{{"XML_ERR_VERSION_MISSING","Malformed declaration expecting version",6},blank(6),blank(23),finish(23)}},
  witness{name:"standalone_adjacent_tail",input:`<?xml version="1.0" encoding="LATIN1" standalone="yes"junk?><a/>`,document:closed("LATIN1",60,Yes),content:closed("LATIN1",0,Yes),contentDetail:customCompletion,events:[]event{finish(54)}},
  witness{name:"unsupported",input:`<?xml version="1.0" encoding="BOGUS"junk?><a/>`,document:closed("BOGUS",0,Absent),content:closed("BOGUS",0,Absent),contentDetail:customCompletion,binaryInvalid:true,binaryLabel:"BOGUS",events:[]event{{"XML_ERR_UNSUPPORTED_ENCODING","Unsupported encoding BOGUS",36}}},
 )
 // Independently captured case variants and a recognized UTF8 handler alias
 // whose spelling does not take the special context-encoding branch.
 for _,label:=range []string{"utf8","utf-8","utf16","utf-16","UTF_8"} {
  input:=`<?xml version="1.0" encoding="`+label+`"standalone="yes"?><a/>`
  tc:=witness{name:label+"_adjacent",input:input,document:closed(label,len(input)-4,Yes),content:closed(label,0,Absent),contentDetail:"Space required.",binaryInvalid:label=="utf16"||label=="utf-16",binaryLabel:label}
  if label=="UTF_8" {tc.events=[]event{blank(36)}}
  cases=append(cases,tc)
 }
 // 49 literals, each exercised in both modes and both input kinds.
 for _,tc:=range cases {
  for _,mode:=range []Mode{Content,Document} {
   for _,kind:=range []InputKind{Text,Binary} {
    t.Run(fmt.Sprintf("%s/mode%d/kind%d",tc.name,mode,kind),func(t *testing.T){
     ctx:=context.Background()
     result,err:=Parse(ctx,tc.input,Options{Mode:mode,InputKind:kind})
     invalid:=kind==Binary&&tc.binaryInvalid
     failed:=invalid||mode==Content&&tc.contentDetail!=""||mode==Document&&len(tc.events)!=0
     declaration:=tc.document;if mode==Content {declaration=tc.content}
     if !failed {
      if err!=nil||!result.valid||result.Original()!=tc.input||result.RequestedMode()!=mode||result.EffectiveMode()!=mode||result.Declaration()!=declaration||len(result.Diagnostics())!=0 {t.Fatalf("accepted result%+v error%v want declaration%+v",result,err,declaration)}
      // Actual typed xml_out/xml_send capture948 supersedes the earlier
      // normalization inference for DOCUMENT's accepted missing separator.
      output:="<a/>";warning:=OutputWarning{}
      if declaration.Standalone==Yes {
       output=`<?xml version="1.0" standalone="yes"?><a/>`
       if tc.contentDetail=="Space required." {output=tc.input;warning=OutputWarning{Message:"could not parse XML declaration in stored value",Detail:"Space required."}}
      }
      if out,w,err:=OutputText(ctx,result);err!=nil||out!=output||w!=warning {t.Fatalf("text%q warning%+v error%v want%q warning%+v",out,w,err,output,warning)}
      if out,w,err:=OutputBinary(ctx,result,6);err!=nil||out!=output||w!=warning {t.Fatalf("binary%q warning%+v error%v want%q warning%+v",out,w,err,output,warning)}
     }else{
      if result.valid||result.original!=""||result.requested!=0||result.effective!=0||result.declaration!=(Declaration{})||result.diagnostics!=nil {t.Fatalf("nonzero failed Result %+v",result)}
      if invalid {
       var failure *EncodingError
       want:=EncodingError{Kind:InvalidEncodingName,Label:tc.binaryLabel,SourceID:-1,DestinationID:6,DestinationName:"UTF8",Message:fmt.Sprintf("invalid encoding name %q",tc.binaryLabel)}
       if !errors.As(err,&failure)||*failure!=want {t.Fatalf("encoding error%+v want%+v",err,want)}
      }else{
       var failure *ParseError
       if !errors.As(err,&failure) {t.Fatalf("parse error%v",err)}
       category:=GrammarError;primary:="invalid XML document";detail:=""
       if mode==Content {category=DeclarationError;primary="invalid XML content: invalid XML declaration";detail=tc.contentDetail;if len(failure.Diagnostics)!=0 {t.Fatalf("custom events%+v",failure.Diagnostics)}}else{
        if len(failure.Diagnostics)!=len(tc.events) {t.Fatalf("events%+v want%+v",failure.Diagnostics,tc.events)}
        blocks:=make([]string,len(tc.events))
        for i,e:=range tc.events {
         caret:=strings.Repeat(" ",e.offset)+"^";if tc.tabCaret {caret=strings.Repeat(" ",37)+"\t^"}
         want:=Diagnostic{Condition:e.condition,Domain:ParserDomain,Severity:Fatal,Line:1,Column:e.offset+1,Offset:e.offset,Message:e.message,SourceContext:tc.input,Caret:caret}
         if failure.Diagnostics[i]!=want {t.Fatalf("event%d %+v want%+v",i,failure.Diagnostics[i],want)}
         blocks[i]="line 1: "+e.message+"\n"+tc.input+"\n"+caret
        }
        detail=strings.Join(blocks,"\n")
       }
       if failure.Category!=category||failure.RequestedMode!=mode||failure.EffectiveMode!=mode||failure.SourceBaseOffset!=0||failure.Declaration!=declaration||failure.Error()!=primary||failure.Detail()!=detail {t.Fatalf("failure%+v detail%q want declaration%+v detail%q",failure,failure.Detail(),declaration,detail)}
      }
      if out,w,err:=OutputText(ctx,result);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("failed text%q warning%+v error%v",out,w,err)}
      if out,w,err:=OutputBinary(ctx,result,6);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("failed binary%q warning%+v error%v",out,w,err)}
     }
     healthy:="<healthy>é</healthy>"
     next,err:=Parse(ctx,healthy,Options{Mode:mode,InputKind:kind})
     if err!=nil||!next.valid||next.Original()!=healthy||next.RequestedMode()!=mode||next.EffectiveMode()!=mode||next.Declaration()!=(Declaration{})||len(next.Diagnostics())!=0 {t.Fatalf("reuse result%+v error%v",next,err)}
     if out,w,err:=OutputText(ctx,next);err!=nil||out!=healthy||w!=(OutputWarning{}) {t.Fatalf("reuse text%q warning%+v error%v",out,w,err)}
     if out,w,err:=OutputBinary(ctx,next,6);err!=nil||out!=healthy||w!=(OutputWarning{}) {t.Fatalf("reuse binary%q warning%+v error%v",out,w,err)}
    })
   }
  }
 }
}
