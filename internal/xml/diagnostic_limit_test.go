package xml

import (
 "context"
 "errors"
 "strings"
 "testing"
)

func TestXMLCoreLimitRecoveryDiagnostics(t *testing.T) {
 // Independent PostgreSQL18.6 UTF8/C captures in artifact467 precede these
 // literals. Conditions, byte offsets and character columns are source-derived
 // from matching Debian libxml2 parser.c, not exposed SQL diagnostic fields.
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
 a78,a80:=strings.Repeat("a",78),strings.Repeat("a",80)
 x80:=strings.Repeat("x",80)
 commentStart:="<a><!--"+strings.Repeat("x",73)
 commentEnd:=strings.Repeat("x",73)+"--></b>"
 piEnd:=strings.Repeat("x",74)+"?></b>"
 cases:=[]witness{
  {"nc_mixed","<"+strings.Repeat("a",50001)+"é/>",[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: NCName",1,50004,50004,a78+"é",79},
   {"XML_ERR_NAME_REQUIRED","StartTag: invalid element name",1,50004,50004,a78+"é",79},
  },true},
  {"nc_eof","<"+strings.Repeat("a",50001),[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: NCName",1,50003,50002,a80,79},
   {"XML_ERR_NAME_REQUIRED","StartTag: invalid element name",1,50003,50002,a80,79},
  },false},
  {"nc_ascii","<"+strings.Repeat("a",50001)+"/>",[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: NCName",1,2,1,"<"+strings.Repeat("a",79),1},
   {"XML_ERR_NAME_REQUIRED","StartTag: invalid element name",1,2,1,"<"+strings.Repeat("a",79),1},
  },true},
  {"name_mixed","<?"+strings.Repeat("a",50001)+"é?><a/>",[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: Name",1,50005,50005,a78+"é",79},
   {"XML_ERR_PI_NOT_STARTED","xmlParsePI : no target name",1,50005,50005,a78+"é",79},
  },true},
  {"name_eof","<?"+strings.Repeat("a",50001),[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: Name",1,50004,50003,a80,79},
   {"XML_ERR_PI_NOT_STARTED","xmlParsePI : no target name",1,50004,50003,a80,79},
  },true},
  {"name_ascii","<?"+strings.Repeat("a",50001)+"?><a/>",[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: Name",1,3,2,"<?"+a78,2},
   {"XML_ERR_PI_NOT_STARTED","xmlParsePI : no target name",1,3,2,"<?"+a78,2},
  },true},
  {"comment_ascii","<a><!--"+strings.Repeat("x",10000001)+"--></b>",[]event{
   {"XML_ERR_COMMENT_NOT_FINISHED","Comment too big found",1,10000009,7,commentStart,7},
   {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",1,20000017,10000015,commentEnd,79},
  },false},
  {"pi_ascii","<a><?go "+strings.Repeat("x",10000001)+"?></b>",[]event{
   {"XML_ERR_PI_NOT_FINISHED","PI go too big found",1,10000010,10000009,x80,79},
   {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",1,10000016,10000015,piEnd,79},
  },false},
  {"comment_segment","<a><!--"+strings.Repeat("x",5000000)+"-"+strings.Repeat("x",5000001)+"--></b>",[]event{
   {"XML_ERR_COMMENT_NOT_FINISHED","Comment too big found",1,10000010,5000007,x80,79},
   {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",1,15000019,10000016,commentEnd,79},
  },false},
  {"comment_long_segment","<a><!--"+strings.Repeat("x",10000123)+"--></b>",[]event{
   {"XML_ERR_COMMENT_NOT_FINISHED","Comment too big found",1,10000131,7,commentStart,7},
   {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",1,20000261,10000137,commentEnd,79},
  },false},
  {"comment_complex_first","<a><!--é"+strings.Repeat("x",10000001)+"--></b>",[]event{
   {"XML_ERR_COMMENT_NOT_FINISHED","Comment too big found",1,10000010,10000010,x80,79},
   {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",1,10000017,10000017,commentEnd,79},
  },false},
  {"comment_complex_transition","<a><!--"+strings.Repeat("x",9999999)+"éxxxxx--></b>",[]event{
   {"XML_ERR_COMMENT_NOT_FINISHED","Comment too big found",1,10000010,10000010,strings.Repeat("x",76)+"éxx",79},
   {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",1,10000020,10000020,strings.Repeat("x",66)+"éxxxxx--></b>",79},
  },false},
  // Raw bytea Detail capture474 preserves the split A9 byte in this context.
  {"comment_complex_scalars","<a><!--"+strings.Repeat("é",5000001)+"--></b>",[]event{
   {"XML_ERR_COMMENT_NOT_FINISHED","Comment too big found",1,5000011,10000011,strings.Repeat("é",39)+"--",79},
   {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",1,5000016,10000016,"\xa9"+strings.Repeat("é",36)+"--></b>",79},
  },false},
  {"pi_scalars","<a><?go "+strings.Repeat("é",5000001)+"?></b>",[]event{
   {"XML_ERR_PI_NOT_FINISHED","PI go too big found",1,5000010,10000010,strings.Repeat("é",40),79},
   {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",1,5000016,10000016,strings.Repeat("é",37)+"?></b>",79},
  },false},
  {"comment_crlf","<a><!--"+strings.Repeat("x",5000000)+"\r\n"+strings.Repeat("x",5000000)+"--></b>",[]event{
   {"XML_ERR_COMMENT_NOT_FINISHED","Comment too big found",2,5000001,5000008,x80,79},
   {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",3,5000008,10000016,commentEnd,79},
  },false},
  {"comment_lf","<a><!--"+strings.Repeat("x",5000000)+"\n"+strings.Repeat("x",5000000)+"--></b>",[]event{
   {"XML_ERR_COMMENT_NOT_FINISHED","Comment too big found",2,5000001,7,commentStart,7},
   {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",3,5000008,10000015,commentEnd,79},
  },false},
  {"comment_lone_cr","<a><!--"+strings.Repeat("x",5000000)+"\r"+strings.Repeat("x",5000000)+"--></b>",[]event{
   {"XML_ERR_COMMENT_NOT_FINISHED","Comment too big found",1,10000011,10000010,strings.Repeat("x",78)+"--",79},
   {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",1,10000016,10000015,commentEnd,79},
  },false},
  {"pi_crlf","<a><?go "+strings.Repeat("x",5000000)+"\r\n"+strings.Repeat("x",5000000)+"?></b>",[]event{
   {"XML_ERR_PI_NOT_FINISHED","PI go too big found",2,5000001,10000010,x80,79},
   {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",2,5000007,10000016,piEnd,79},
  },false},
  // Independent NmToken/QName fallback capture484; the failed token is not
  // the retained opening name, and the first attribute attempt is observable.
  {"nmtoken_fallback","<a:"+strings.Repeat("1",50002)+"/>",[]event{
   {"XML_ERR_NAME_TOO_LONG","Name too long: NmToken",1,50005,50004,strings.Repeat("1",80),79},
   {"XML_ERR_NAME_REQUIRED","error parsing attribute name",1,50005,50004,strings.Repeat("1",80),79},
   {"XML_ERR_SPACE_REQUIRED","attributes construct error",1,50005,50004,strings.Repeat("1",80),79},
   {"XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag a: line 1",1,50005,50004,strings.Repeat("1",80),79},
  },true},
 }
 for _,m:=range []Mode{Content,Document} {
  for _,tc:=range cases {
   t.Run(tc.name+map[Mode]string{Content:"/content",Document:"/document"}[m],func(t *testing.T){
    events:=append([]event(nil),tc.events...)
    if m==Document&&tc.documentExtra {
     last:=events[len(events)-1]
     if !strings.HasPrefix(tc.name,"name_") {
      last.condition="XML_ERR_DOCUMENT_END";last.message="Extra content at the end of the document"
     }else{
      last.condition="XML_ERR_DOCUMENT_EMPTY";last.message="Start tag expected, '<' not found"
     }
     events=append(events,last)
    }
    for _,kind:=range []InputKind{Text,Binary} {
     r,err:=Parse(context.Background(),tc.input,Options{Mode:m,InputKind:kind})
     var e *ParseError
     if !errors.As(err,&e)||r.valid||r.original!=""||r.requested!=0||r.effective!=0||r.declaration!=(Declaration{})||len(r.diagnostics)!=0 {t.Fatalf("result%+v error%v",r,err)}
     primary:="invalid XML content";if m==Document {primary="invalid XML document"}
     // Render independently specified blocks, never the returned diagnostics.
     details:=make([]string,len(events))
     for i,v:=range events {details[i]=blocks(v.line,v.source,v.caret,v.message)}
     wantDetail:=strings.Join(details,"\n")
     if e.Error()!=primary||e.RequestedMode!=m||e.EffectiveMode!=m||e.Category!=GrammarError||e.SourceBaseOffset!=0||e.Declaration!=(Declaration{})||e.Detail()!=wantDetail||len(e.Diagnostics)!=len(events) {t.Fatalf("%+v detail%q want%q",e,e.Detail(),wantDetail)}
     for i,v:=range events {
      want:=Diagnostic{Condition:v.condition,Domain:ParserDomain,Severity:Fatal,Line:v.line,Column:v.column,Offset:v.offset,Message:v.message,SourceContext:v.source,Caret:strings.Repeat(" ",v.caret)+"^"}
      if e.Diagnostics[i]!=want {t.Fatalf("event%d: %+v want %+v",i,e.Diagnostics[i],want)}
     }
     accept(t,"<healthy>é</healthy>",m)
    }
   })
  }
 }
}
