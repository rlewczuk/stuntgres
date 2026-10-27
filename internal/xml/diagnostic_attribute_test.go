package xml

import (
 "context"
 "errors"
 "fmt"
 "strings"
 "testing"
)

func TestXMLCoreAttributeConstructionLimits(t *testing.T) {
 // Independent pinned PostgreSQL18.6 UTF8/C captures in
 // local://xml-core-attribute-limit-captures.txt and
 // local://xml-core-attribute-supplement-root.txt precede these literals.
 // Binary UTF8 routes and structured fields are source-derived from matching
 // Debian libxml2 parser.c3920–4155,9130–9178,12539–12565 and
 // parserInternals.c104–130,576–732, not SQL fields. Halted input keeps the
 // provider's original-source Offset, not the native empty sentinel's cur-base.
 type event struct {condition Condition;message string;offset,column int;source string;caret int}
 type witness struct {name,input string;events []event}
 for _,mode:=range []Mode{Content,Document} {
  cases:=[]witness{}
  for _,reference:=range []struct{name,value string}{{"predefined","&amp;"},{"numeric","&#38;"}} {
   for _,n:=range []int{9999994,9999995,9999996,9999999,10000000} {
    tc:=witness{name:fmt.Sprintf("%s_%d",reference.name,n),input:`<a x="`+strings.Repeat("x",n)+reference.value+`">`+strings.Repeat("y",300)+`</a>`}
    contribution:=1;if mode==Content {contribution=5}
    if n+contribution>10000000 {
     source:=strings.Repeat("x",75)+reference.value
     offset:=n+11
     tc.events=[]event{
      {"XML_ERR_ATTRIBUTE_NOT_FINISHED","AttValue length too long",offset,offset+1,source,79},
      {"XML_ERR_NO_MEMORY","Memory allocation failed",offset,offset+1,source,79},
      {"XML_ERR_SPACE_REQUIRED","attributes construct error",offset,offset+1,source,79},
      {"XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag a line 1",offset,offset+1,source,79},
     }
     if mode==Document {tc.events=append(tc.events,event{"XML_ERR_DOCUMENT_END","Extra content at the end of the document",offset,offset+1,source,79})}
    }
    cases=append(cases,tc)
   }
  }
  for _,n:=range []int{9999998,9999999} {
   for _,tail:=range []struct{name,value string}{{"fffe","\uFFFE"},{"valid","z"},{"control","\x01"},{"quote",""}} {
    for _,padded:=range []bool{false,true} {
     ending:=`"/>`;suffix:="short"
     if padded {ending=`">`+strings.Repeat("y",300)+`</a>`;suffix="padded"}
     tc:=witness{name:fmt.Sprintf("slow_%d_%s_%s",n,tail.name,suffix),input:`<a x="é`+strings.Repeat("x",n)+tail.value+ending}
     source:=strings.Repeat("x",80)
     offset:=n+8
     add:=func(condition Condition,message string){tc.events=append(tc.events,event{condition,message,offset,offset,source,79})}
     if mode==Content&&!padded {
      // Complex GROW owns the failure before next decode/length checking.
      tc.events=[]event{
       {"XML_ERR_INTERNAL_ERROR","internal error: Huge input lookup",10000001,10000001,source,79},
       {"XML_ERR_SPACE_REQUIRED","attributes construct error",10000001,10000001,"",0},
       {"XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag a line 1",10000001,10000001,"",0},
      }
     }else if n==9999998&&tail.name=="quote" {
      // Exactly ten million constructed bytes, with a valid closing quote.
     }else{
      if n==9999998&&tail.name=="valid" {offset++;source=strings.Repeat("x",79)+"z"}
      if tail.name=="fffe" {add("XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range")}
      if n==9999999||tail.name=="valid" {
       add("XML_ERR_ATTRIBUTE_NOT_FINISHED","AttValue length too long")
       add("XML_ERR_NO_MEMORY","Memory allocation failed")
      }else{add("XML_ERR_INVALID_CHAR","invalid character in attribute value")}
      add("XML_ERR_SPACE_REQUIRED","attributes construct error")
      add("XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag a line 1")
      if mode==Document {add("XML_ERR_DOCUMENT_END","Extra content at the end of the document")}else if tail.name=="fffe" {
       add("XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range")
       add("XML_ERR_INVALID_CHAR","PCDATA invalid Char value 65534")
      }else if tail.name=="control" {add("XML_ERR_INVALID_CHAR","PCDATA invalid Char value 1")}
     }
     cases=append(cases,tc)
    }
   }
  }
  for _,reference:=range []struct{name,value string}{{"predefined","&amp;"},{"numeric","&#38;"}} {
   for _,n:=range []int{9999995,9999996} {
    tc:=witness{name:fmt.Sprintf("namespace_%s_%d",reference.name,n),input:`<a xmlns:p="`+strings.Repeat("x",n)+reference.value+`">`+strings.Repeat("y",300)+`</a>`}
    if mode==Content&&n==9999996 {
     offset:=n+17;source:=strings.Repeat("x",75)+reference.value
     tc.events=[]event{
      {"XML_ERR_ATTRIBUTE_NOT_FINISHED","AttValue length too long",offset,offset+1,source,79},
      {"XML_ERR_NO_MEMORY","Memory allocation failed",offset,offset+1,source,79},
      {"XML_ERR_SPACE_REQUIRED","attributes construct error",offset,offset+1,source,79},
      {"XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag a line 1",offset,offset+1,source,79},
     }
    }
    cases=append(cases,tc)
   }
  }
  for _,reference:=range []struct{name,value string}{{"predefined","&amp;"},{"numeric_amp","&#38;"},{"numeric_ascii","&#65;"}} {
   for _,tail:=range []struct{name,value string}{{"fffe","\uFFFE"},{"valid","z"},{"control","\x01"}} {
    tc:=witness{name:"reference_next_"+reference.name+"_"+tail.name,input:`<a x="é`+strings.Repeat("x",9999998)+reference.value+tail.value+`">`+strings.Repeat("y",300)+`</a>`}
    source:=strings.Repeat("x",75)+reference.value
    add:=func(condition Condition,message string){tc.events=append(tc.events,event{condition,message,10000011,10000011,source,79})}
    if tail.name=="fffe" {add("XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range")}
    add("XML_ERR_ATTRIBUTE_NOT_FINISHED","AttValue length too long")
    add("XML_ERR_NO_MEMORY","Memory allocation failed")
    add("XML_ERR_SPACE_REQUIRED","attributes construct error")
    add("XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag a line 1")
    if mode==Document {add("XML_ERR_DOCUMENT_END","Extra content at the end of the document")}else if tail.name=="fffe" {
     add("XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range")
     add("XML_ERR_INVALID_CHAR","PCDATA invalid Char value 65534")
    }else if tail.name=="control" {add("XML_ERR_INVALID_CHAR","PCDATA invalid Char value 1")}
    cases=append(cases,tc)
   }
  }
  for _,tc:=range cases {
   t.Run(tc.name+map[Mode]string{Content:"/content",Document:"/document"}[mode],func(t *testing.T){
    for _,kind:=range []InputKind{Text,Binary} {
     ctx:=context.Background()
     result,err:=Parse(ctx,tc.input,Options{Mode:mode,InputKind:kind})
     if len(tc.events)==0 {
      if err!=nil||!result.valid||result.Original()!=tc.input||result.RequestedMode()!=mode||result.EffectiveMode()!=mode||result.Declaration()!=(Declaration{})||len(result.Diagnostics())!=0 {t.Fatalf("kind%d accepted valid%t error%v",kind,result.valid,err)}
      if out,w,err:=OutputText(ctx,result);err!=nil||out!=tc.input||w!=(OutputWarning{}) {t.Fatalf("kind%d accepted text length%d error%v",kind,len(out),err)}
      if out,w,err:=OutputBinary(ctx,result,6);err!=nil||out!=tc.input||w!=(OutputWarning{}) {t.Fatalf("kind%d accepted binary length%d error%v",kind,len(out),err)}
     }else{
      var failure *ParseError
      if !errors.As(err,&failure)||result.valid||result.original!=""||result.requested!=0||result.effective!=0||result.declaration!=(Declaration{})||result.diagnostics!=nil {t.Fatalf("kind%d failure valid%t error%v",kind,result.valid,err)}
      primary:="invalid XML content";if mode==Document {primary="invalid XML document"}
      detail:=make([]string,0,len(tc.events))
      if len(failure.Diagnostics)!=len(tc.events) {t.Fatalf("kind%d diagnostics%+v want%+v",kind,failure.Diagnostics,tc.events)}
      for i,v:=range tc.events {
       caret:=strings.Repeat(" ",v.caret)+"^"
       want:=Diagnostic{Condition:v.condition,Domain:ParserDomain,Severity:Fatal,Line:1,Column:v.column,Offset:v.offset,Message:v.message,SourceContext:v.source,Caret:caret}
       if failure.Diagnostics[i]!=want {t.Fatalf("kind%d event%d %+v want%+v",kind,i,failure.Diagnostics[i],want)}
       detail=append(detail,"line 1: "+v.message+"\n"+v.source+"\n"+caret)
      }
      if failure.Error()!=primary||failure.RequestedMode!=mode||failure.EffectiveMode!=mode||failure.Category!=GrammarError||failure.SourceBaseOffset!=0||failure.Declaration!=(Declaration{})||failure.Detail()!=strings.Join(detail,"\n") {t.Fatalf("kind%d error%+v detail%q want%q",kind,failure,failure.Detail(),strings.Join(detail,"\n"))}
      if out,w,err:=OutputText(ctx,result);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("kind%d failed text%q error%v",kind,out,err)}
      if out,w,err:=OutputBinary(ctx,result,6);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("kind%d failed binary%q error%v",kind,out,err)}
     }
     healthy:=`<a xmlns:p="u" xmlns:q="u" p:x="1" q:x="2" q:x="3"/>`
     next,err:=Parse(ctx,healthy,Options{Mode:mode,InputKind:kind})
     if err!=nil||!next.valid||next.Original()!=healthy||next.RequestedMode()!=mode||next.EffectiveMode()!=mode||next.Declaration()!=(Declaration{})||len(next.Diagnostics())!=0 {t.Fatalf("kind%d healthy reuse%+v error%v",kind,next,err)}
     if out,w,err:=OutputText(ctx,next);err!=nil||out!=healthy||w!=(OutputWarning{}) {t.Fatalf("kind%d healthy text%q error%v",kind,out,err)}
     if out,w,err:=OutputBinary(ctx,next,6);err!=nil||out!=healthy||w!=(OutputWarning{}) {t.Fatalf("kind%d healthy binary%q error%v",kind,out,err)}
    }
   })
  }
 }
}

func TestXMLCoreAttributeCRLFRead(t *testing.T) {
 // Independent PostgreSQL18.6 UTF8/C captures: artifact779 (URI text and raw
 // binary receive guards), local://xml-core-attribute-crlf-evidence.md (boundary corpus).
 // Structured fields and BinaryUTF8 equivalence use matching Debian libxml2
 // parser.c complex AttValue/NEXTL and parserInternals.c CUR_CHAR, not SQL fields.
 diagnostic:=func(condition Condition,message string,line,column,offset int,source string,caret int) Diagnostic {
  return Diagnostic{Condition:condition,Domain:ParserDomain,Severity:Fatal,Line:line,Column:column,Offset:offset,Message:message,SourceContext:source,Caret:strings.Repeat(" ",caret)+"^"}
 }
 for _,mode:=range []Mode{Content,Document} {
  run:=func(name,input string,want []Diagnostic,kinds ...InputKind) {
   t.Helper()
   t.Run(name+map[Mode]string{Content:"/content",Document:"/document"}[mode],func(t *testing.T){
    if len(kinds)==0 {kinds=[]InputKind{Text,Binary}}
    for _,kind:=range kinds {
     ctx:=context.Background()
     result,err:=Parse(ctx,input,Options{Mode:mode,InputKind:kind})
     if len(want)==0 {
      if err!=nil||!result.valid||result.Original()!=input||result.RequestedMode()!=mode||result.EffectiveMode()!=mode||result.Declaration()!=(Declaration{})||len(result.Diagnostics())!=0 {t.Fatalf("kind%d accepted valid%t error%v",kind,result.valid,err)}
      if out,w,err:=OutputText(ctx,result);err!=nil||out!=input||w!=(OutputWarning{}) {t.Fatalf("kind%d accepted text length%d error%v",kind,len(out),err)}
      if out,w,err:=OutputBinary(ctx,result,6);err!=nil||out!=input||w!=(OutputWarning{}) {t.Fatalf("kind%d accepted binary length%d error%v",kind,len(out),err)}
     }else{
      var failure *ParseError
      if !errors.As(err,&failure)||result.valid||result.original!=""||result.requested!=0||result.effective!=0||result.declaration!=(Declaration{})||result.diagnostics!=nil {t.Fatalf("kind%d failure valid%t error%v",kind,result.valid,err)}
      if len(failure.Diagnostics)!=len(want) {t.Fatalf("kind%d diagnostics%+v want%+v",kind,failure.Diagnostics,want)}
      detail:=make([]string,len(want))
      for i,d:=range want {
       if failure.Diagnostics[i]!=d {t.Fatalf("kind%d event%d %+v want%+v",kind,i,failure.Diagnostics[i],d)}
       detail[i]=fmt.Sprintf("line %d: %s\n%s\n%s",d.Line,d.Message,d.SourceContext,d.Caret)
      }
      primary:="invalid XML content";if mode==Document {primary="invalid XML document"}
      if failure.Error()!=primary||failure.RequestedMode!=mode||failure.EffectiveMode!=mode||failure.Category!=GrammarError||failure.SourceBaseOffset!=0||failure.Declaration!=(Declaration{})||failure.Detail()!=strings.Join(detail,"\n") {t.Fatalf("kind%d error%+v detail%q want%q",kind,failure,failure.Detail(),strings.Join(detail,"\n"))}
      if out,w,err:=OutputText(ctx,result);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("kind%d failed text%q error%v",kind,out,err)}
      if out,w,err:=OutputBinary(ctx,result,6);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("kind%d failed binary%q error%v",kind,out,err)}
     }
     healthy:=`<a xmlns:p="u" xmlns:q="u" p:x="1" q:x="2" q:x="3"/>`
     next,err:=Parse(ctx,healthy,Options{Mode:mode,InputKind:kind})
     if err!=nil||!next.valid||next.Original()!=healthy||next.RequestedMode()!=mode||next.EffectiveMode()!=mode||next.Declaration()!=(Declaration{})||len(next.Diagnostics())!=0 {t.Fatalf("kind%d healthy reuse%+v error%v",kind,next,err)}
     if out,w,err:=OutputText(ctx,next);err!=nil||out!=healthy||w!=(OutputWarning{}) {t.Fatalf("kind%d healthy text%q error%v",kind,out,err)}
     if out,w,err:=OutputBinary(ctx,next,6);err!=nil||out!=healthy||w!=(OutputWarning{}) {t.Fatalf("kind%d healthy binary%q error%v",kind,out,err)}
    }
   })
  }
  for _,ending:=range []struct{name,value string}{{"crlf","\r\n"},{"cr","\r"},{"lf","\n"}} {
   line:=2;if ending.name=="cr" {line=1}
   afterColumn:=func(q int) int {if ending.name=="cr" {return q+1};return 1}
   lengthEvents:=func(q,level int,tail,source string) []Diagnostic {
    var want []Diagnostic
    add:=func(condition Condition,message string,l,c,at int,context string,caret int) {
     want=append(want,diagnostic(condition,message,l,c,at,context,caret))
    }
    // q and level come from the independent below/exact/above construction
    // table, not the parser's constructed counter or context renderer.
    if level==0&&tail=="" {return nil}
    context:=`">`+strings.Repeat("y",78);caret:=0
    if tail!="" {context=`z">`+strings.Repeat("y",77)}
    at,column:=q+len(ending.value),afterColumn(q)
    if level==0 {at++;column++;caret=1}
    if level==2 {
     readAt:=q;if ending.name=="crlf" {readAt++}
     add("XML_ERR_ATTRIBUTE_NOT_FINISHED","AttValue length too long",1,q,readAt,source,79)
     add("XML_ERR_NO_MEMORY","Memory allocation failed",1,q,readAt,source,79)
     if tail=="" {
      add("XML_ERR_NAME_REQUIRED","error parsing attribute name",line,column,at,context,0)
     }else{
      at++;column++;caret=1
      add("XML_ERR_ATTRIBUTE_WITHOUT_VALUE","Specification mandates value for attribute z",line,column,at,context,caret)
     }
    }else{
     add("XML_ERR_ATTRIBUTE_NOT_FINISHED","AttValue length too long",line,column,at,context,caret)
     add("XML_ERR_NO_MEMORY","Memory allocation failed",line,column,at,context,caret)
    }
    add("XML_ERR_SPACE_REQUIRED","attributes construct error",line,column,at,context,caret)
    add("XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag a line 1",line,column,at,context,caret)
    if mode==Document {add("XML_ERR_DOCUMENT_END","Extra content at the end of the document",line,column,at,context,caret)}
    return want
   }
   for _,family:=range []struct{name,reference string;first int}{{"ordinary","",9999997},{"reference","&#65;",9999996}} {
    for level:=range 3 {
     n:=family.first+level;q:=8+n+len(family.reference)
     source:=strings.Repeat("x",80)
     if family.reference!="" {source=strings.Repeat("x",76)+family.reference[:4]}
     for _,tail:=range []string{"","z"} {
      input:=`<a x="é`+strings.Repeat("x",n)+family.reference+ending.value+tail+`">`+strings.Repeat("y",300)+`</a>`
      run(fmt.Sprintf("%s_%d_%s_%s",family.name,n,ending.name,tail),input,lengthEvents(q,level,tail,source))
     }
    }
   }
   for _,reference:=range []string{"&amp;","&#38;"} {
    input:=`<a x="é`+strings.Repeat("x",9999998)+reference+ending.value+`">`+strings.Repeat("y",300)+`</a>`
    run("amp_"+reference+"_"+ending.name,input,lengthEvents(10000011,2,"",strings.Repeat("x",76)+reference[:4]))
   }
   for _,tail:=range []string{"","&#65;","&bad;","\uFFFE"} {
    q:=10000004;at,column:=q+len(ending.value),afterColumn(q)
    var want []Diagnostic
    if tail=="&bad;" {
     want=[]Diagnostic{diagnostic("XML_ERR_UNDECLARED_ENTITY","Entity 'bad' not defined",line,column+5,at+5,`&bad;">`+strings.Repeat("y",73),5)}
    }else if tail=="\uFFFE" {
     source:="\uFFFE"+`">`+strings.Repeat("y",75)
     add:=func(condition Condition,message string){want=append(want,diagnostic(condition,message,line,column,at,source,0))}
     add("XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range")
     add("XML_ERR_INVALID_CHAR","invalid character in attribute value")
     add("XML_ERR_SPACE_REQUIRED","attributes construct error")
     add("XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag a line 1")
     if mode==Document {add("XML_ERR_DOCUMENT_END","Extra content at the end of the document")}else{
      add("XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range")
      add("XML_ERR_INVALID_CHAR","PCDATA invalid Char value 65534")
     }
    }
    run("cached_"+ending.name+"_"+tail,`<a x="é`+strings.Repeat("x",9999996)+ending.value+tail+`">`+strings.Repeat("y",300)+`</a>`,want)
   }
   for _,uri:=range []struct{name,source,plain string}{
    {"literal","u"+ending.value+"v","u v"},
    {"reference","u"+ending.value+"&#118;","u v"},
    {"gap","u"+ending.value+"&#0;v","u v"},
    {"initial",ending.value+"uv"," uv"},
   } {
    var want []Diagnostic
    if uri.name=="gap" {
     column:=5;if ending.name=="cr" {column=19}
     want=[]Diagnostic{diagnostic("XML_ERR_INVALID_CHAR","xmlParseCharRef: invalid xmlChar value 0",line,column,17+len(ending.value),`&#0;v" xmlns:q="u v" p:x="1" q:x="2" q:x="3"/>`,4)}
    }
    run("uri_first_"+ending.name+"_"+uri.name,`<a xmlns:p="`+uri.source+`" xmlns:q="`+uri.plain+`" p:x="1" q:x="2" q:x="3"/>`,want)
   }
   for _,uri:=range []struct{name,plain,source string}{
    {"after_reference","uv w","u&#118;"+ending.value+"w"},
    {"first"," uv",ending.value+"uv"},
    {"after_literal","u v","u"+ending.value+"v"},
   } {
    run("uri_"+ending.name+"_"+uri.name,`<a xmlns:p="`+uri.plain+`" xmlns:q="`+uri.source+`" p:x="1" q:x="2" q:x="3"/>`,nil)
   }
   for _,reference:=range []string{"","&#65;"} {
    prefix:=`<a x="`+"\xff"+reference
    bytes:="0xFF 0x26 0x23 0x36"
    if reference=="" {
     switch ending.name {
     case "crlf":bytes="0xFF 0x0D 0x0A 0x26"
     case "cr":bytes="0xFF 0x0D 0x26 0x62"
     case "lf":bytes="0xFF 0x0A 0x26 0x62"
     }
    }
    line,column:=2,6
    if ending.name=="cr" {line=1;column=14+len(reference)}
    run("failed_single_byte_"+ending.name+fmt.Sprint(len(reference)),prefix+ending.value+`&bad;"/>`,[]Diagnostic{
     diagnostic("XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: "+bytes,1,7,6,prefix,6),
     diagnostic("XML_ERR_UNDECLARED_ENTITY","Entity 'bad' not defined",line,column,len(prefix)+len(ending.value)+5,`&bad;"/>`,5),
    },Binary)
   }
  }
 }
}
