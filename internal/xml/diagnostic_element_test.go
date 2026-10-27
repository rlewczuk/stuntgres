package xml

import (
 "context"
 "errors"
 "strings"
 "testing"
)

func TestXMLCoreElementRecoveryDiagnostics(t *testing.T) {
 // Independent PostgreSQL18.6 UTF8/C captures564 precede these literals.
 // Detail, context and byte carets are observed; conditions and tracked columns
 // follow the matching Debian libxml2 productions, not SQL-exposed metadata.
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
  documentEvents []event
 }
 // These bounded ASCII rows have column=offset+1, full-input context and byte
 // caret=offset. Multibyte and newline rows explicitly supply separate fields.
 ascii:=func(condition Condition,message string,offset int) event {
  return event{condition,message,1,offset+1,offset,"",offset}
 }
 mismatch:=func(message string,offset int) event {return ascii("XML_ERR_TAG_NAME_MISMATCH",message,offset)}
 missing:=func(offset int) event {return ascii("XML_ERR_ATTRIBUTE_WITHOUT_VALUE","Specification mandates value for attribute x",offset)}
 quote:=func(offset int) event {return ascii("XML_ERR_ATTRIBUTE_NOT_STARTED","AttValue: \" or ' expected",offset)}
 construct:=func(offset int) event {return ascii("XML_ERR_SPACE_REQUIRED","attributes construct error",offset)}
 unfinished:=func(offset int) event {return ascii("XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag a line 1",offset)}
 gt:=func(offset int) event {return ascii("XML_ERR_GT_REQUIRED","expected '>'",offset)}
 cases:=[]witness{
  {name:"local_mismatch",input:"<p:a></q:b>",events:[]event{mismatch("Opening and ending tag mismatch: a line 1 and b",11)}},
  {name:"local_eof",input:"<p:a>",events:[]event{ascii("XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag a line 1",5)}},
  {name:"local_attr",input:"<a p:x/>",events:[]event{missing(6)}},
  {name:"ordinary_name",input:"<a></b:c:1>",events:[]event{mismatch("Opening and ending tag mismatch: a line 1 and b:c:1",11)}},
  {name:"null_end",input:"<a></>",events:[]event{mismatch("Opening and ending tag mismatch: a line 1 and unparsable",6)}},
  {name:"missing_gt_mismatch",input:"<a></b!>",events:[]event{gt(6),mismatch("Opening and ending tag mismatch: a line 1 and b",6)},documentExtra:true},
  {name:"byte_column",input:"<é></é>&bad;",events:[]event{
   {"XML_ERR_UNDECLARED_ENTITY","Entity 'bad' not defined",1,14,14,"<é></é>&bad;",14},
  },documentEvents:[]event{
   {"XML_ERR_DOCUMENT_END","Extra content at the end of the document",1,9,9,"<é></é>&bad;",9},
  }},
  {name:"attr_missing_eq",input:"<a x/>",events:[]event{missing(4)}},
  {name:"attr_missing_quote",input:"<a x=/>",events:[]event{quote(5)}},
  {name:"prefix_mismatch_same_local",input:"<p:a></q:a>",events:[]event{mismatch("Opening and ending tag mismatch: a line 1 and a",11)}},
  {name:"prefix_missing_end",input:"<p:a></a>",events:[]event{mismatch("Opening and ending tag mismatch: a line 1 and a",9)}},
  {name:"prefix_extra_end",input:"<a></p:a>",events:[]event{mismatch("Opening and ending tag mismatch: a line 1 and p:a",9)}},
  {name:"prefix_null_end",input:"<p:a></>",events:[]event{mismatch("Opening and ending tag mismatch: a line 1 and unparsable",8)}},
  {name:"prefix_ordinary_fallback",input:"<p:a></q:b:c>",events:[]event{mismatch("Opening and ending tag mismatch: a line 1 and b:c",13)}},
  {name:"missing_gt_matched",input:"<a></a!>",events:[]event{gt(6)},documentExtra:true},
  {name:"prefix_missing_gt_matched",input:"<p:a></p:a!>",events:[]event{gt(10)},documentExtra:true},
  {name:"prefix_byte_column",input:"<p:é></p:é>&bad;",events:[]event{
   {"XML_ERR_UNDECLARED_ENTITY","Entity 'bad' not defined",1,18,18,"<p:é></p:é>&bad;",18},
  },documentEvents:[]event{
   {"XML_ERR_DOCUMENT_END","Extra content at the end of the document",1,13,13,"<p:é></p:é>&bad;",13},
  }},
  {name:"match_space",input:"<é></é \n>&bad;",events:[]event{
   {"XML_ERR_UNDECLARED_ENTITY","Entity 'bad' not defined",2,7,16,">&bad;",6},
  },documentEvents:[]event{
   {"XML_ERR_DOCUMENT_END","Extra content at the end of the document",2,2,11,">&bad;",1},
  }},
  {name:"attr_gt",input:"<a x></a>",events:[]event{missing(4)}},
  {name:"attr_quote_gt",input:"<a x=></a>",events:[]event{quote(5)}},
  {name:"attr_missing_eq_space",input:`<a x y="v"/>`,events:[]event{missing(5),construct(5),unfinished(5)},documentExtra:true},
  {name:"attr_missing_quote_space",input:`<a x= y="v"/>`,events:[]event{quote(6),construct(6),unfinished(6)},documentExtra:true},
  {name:"attr_no_separator",input:`<a x="v"y="w"/>`,events:[]event{construct(8),unfinished(8)},documentExtra:true},
  {name:"attr_failure_non_delimiter",input:"<a x!/>",events:[]event{missing(4),construct(4),unfinished(4)},documentExtra:true},
  {name:"attr_failure_space_delimiter",input:"<a x= />",events:[]event{quote(6)}},
  {name:"attr_failed_name",input:"<a !/>",events:[]event{ascii("XML_ERR_NAME_REQUIRED","error parsing attribute name",3),construct(3),unfinished(3)},documentExtra:true},
  {name:"attr_malformed_then_entity",input:"<a x/> &bad;",events:[]event{missing(4),ascii("XML_ERR_UNDECLARED_ENTITY","Entity 'bad' not defined",12)},documentEvents:[]event{missing(4),ascii("XML_ERR_DOCUMENT_END","Extra content at the end of the document",7)}},
  {name:"name_prefix_terminator",input:"<ab></abc>",events:[]event{mismatch("Opening and ending tag mismatch: ab line 1 and abc",10)}},
  // Independent supplemental captures568: terminal/null fallback and the
  // complete post-attribute duplicate diagnostic production.
  {name:"eof_null",input:"<a></",events:[]event{gt(5),mismatch("Opening and ending tag mismatch: a line 1 and unparsable",5)}},
  {name:"eof_name",input:"<a></b",events:[]event{gt(6),mismatch("Opening and ending tag mismatch: a line 1 and b",6)}},
  {name:"eof_prefix_null",input:"<p:a></",events:[]event{gt(7),mismatch("Opening and ending tag mismatch: a line 1 and unparsable",7)}},
  {name:"duplicate_prefixed",input:`<a p:x="1" p:x="2"/>`,events:[]event{ascii("XML_ERR_ATTRIBUTE_REDEFINED","Attribute p:x redefined",18)}},
  {name:"duplicate_before_third",input:`<a x="1" x="2" y="3"/>`,events:[]event{ascii("XML_ERR_ATTRIBUTE_REDEFINED","Attribute x redefined",20)}},
  {name:"duplicate_broken_separator",input:`<a x="1" x="2"y="3"/>`,events:[]event{construct(14),ascii("XML_ERR_ATTRIBUTE_REDEFINED","Attribute x redefined",14),unfinished(14)},documentExtra:true},
  {name:"duplicate_bad_scalar",input:"<a x=\"1\" x=\"\x01\"/>",events:[]event{
   ascii("XML_ERR_INVALID_CHAR","invalid character in attribute value",12),construct(12),ascii("XML_ERR_ATTRIBUTE_REDEFINED","Attribute x redefined",12),unfinished(12),ascii("XML_ERR_INVALID_CHAR","PCDATA invalid Char value 1",12),
  },documentEvents:[]event{
   ascii("XML_ERR_INVALID_CHAR","invalid character in attribute value",12),construct(12),ascii("XML_ERR_ATTRIBUTE_REDEFINED","Attribute x redefined",12),unfinished(12),ascii("XML_ERR_DOCUMENT_END","Extra content at the end of the document",12),
  }},
  {name:"duplicate_bad_lt",input:`<a x="1" x="<"/>`,events:[]event{
   ascii("XML_ERR_LT_IN_ATTRIBUTE","Unescaped '<' not allowed in attributes values",12),construct(12),ascii("XML_ERR_ATTRIBUTE_REDEFINED","Attribute x redefined",12),unfinished(12),ascii("XML_ERR_NAME_REQUIRED","StartTag: invalid element name",13),
  },documentEvents:[]event{
   ascii("XML_ERR_LT_IN_ATTRIBUTE","Unescaped '<' not allowed in attributes values",12),construct(12),ascii("XML_ERR_ATTRIBUTE_REDEFINED","Attribute x redefined",12),unfinished(12),ascii("XML_ERR_DOCUMENT_END","Extra content at the end of the document",12),
  }},
  {name:"attribute_failed_separator_progress",input:`<a x="v" 1 y="w"/>`,events:[]event{ascii("XML_ERR_NAME_REQUIRED","error parsing attribute name",9),construct(9),unfinished(9)},documentExtra:true},
  {name:"prefixed_end_bad_local",input:"<p:a></q:b:1>",events:[]event{gt(11),mismatch("Opening and ending tag mismatch: a line 1 and b:",11)},documentExtra:true},
  // Captures577 distinguish namespace duplicate timing from ordinary attrs,
  // and physical EOF from a supplied delimiter after each failed production.
  {name:"ns_prefix_duplicate",input:`<a xmlns:p="u" xmlns:p="v" y="3"/>`,events:[]event{ascii("XML_ERR_ATTRIBUTE_REDEFINED","Attribute xmlns:p redefined",26)}},
  {name:"ns_default_duplicate_separator",input:`<a xmlns="u" xmlns="v"y="3"/>`,events:[]event{ascii("XML_ERR_ATTRIBUTE_REDEFINED","Attribute xmlns redefined",22),construct(22),unfinished(22)},documentExtra:true},
  {name:"attr_eof_name",input:"<a x",events:[]event{missing(4),construct(4),unfinished(4)}},
  {name:"attr_eof_empty_value",input:"<a x='",events:[]event{ascii("XML_ERR_ATTRIBUTE_NOT_FINISHED","AttValue: ' expected",6),construct(6),unfinished(6)}},
  {name:"attr_eof_value",input:"<a x='u",events:[]event{ascii("XML_ERR_ATTRIBUTE_NOT_FINISHED","AttValue: ' expected",7),construct(7),unfinished(7)}},
 }
 for _,m:=range []Mode{Content,Document} {
  for _,tc:=range cases {
   t.Run(tc.name+map[Mode]string{Content:"/content",Document:"/document"}[m],func(t *testing.T){
    events:=append([]event(nil),tc.events...)
    if m==Document&&tc.documentEvents!=nil {events=append([]event(nil),tc.documentEvents...)}
    if m==Document&&tc.documentExtra {
     last:=events[len(events)-1]
     last.condition="XML_ERR_DOCUMENT_END";last.message="Extra content at the end of the document"
     events=append(events,last)
    }
    details:=make([]string,len(events))
    for i:=range events {
     if events[i].source=="" {events[i].source=tc.input}
     v:=events[i];details[i]=blocks(v.line,v.source,v.caret,v.message)
    }
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
