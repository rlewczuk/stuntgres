package xml

import (
 "context"
 "errors"
 "strings"
 "testing"
)

func TestXMLCoreDiagnostics(t *testing.T) {
 for _,m:=range []Mode{Content,Document} {
  s:=`<a x="1" x="2"/>`;e:=reject(t,s,m)
  want:=Diagnostic{Condition:"XML_ERR_ATTRIBUTE_REDEFINED",Domain:ParserDomain,Severity:Fatal,Line:1,Column:15,Offset:14,Message:"Attribute x redefined",SourceContext:s,Caret:"              ^"}
  if len(e.Diagnostics)!=1||e.Diagnostics[0]!=want||e.Detail()!=want.String(){t.Fatalf("duplicate %+v",e.Diagnostics)}
  s=`<twoerrors>&idontexist;</unbalanced>`;e=reject(t,s,m)
  if e.Detail()!=blocks(1,s,23,"Entity 'idontexist' not defined")+"\n"+blocks(1,s,36,"Opening and ending tag mismatch: twoerrors line 1 and unbalanced") {t.Fatal(e.Detail())}
  if len(e.Diagnostics)!=2||e.Diagnostics[0].Condition!="XML_ERR_UNDECLARED_ENTITY"||e.Diagnostics[1].Condition!="XML_ERR_TAG_NAME_MISMATCH"||e.Diagnostics[0].Column!=24||e.Diagnostics[1].Column!=37 {t.Fatal(e.Diagnostics)}
  for _,tc:=range []struct{ref string;caret int;messages []string}{
   {"&#1;",7,[]string{"xmlParseCharRef: invalid xmlChar value 1"}},
   {"&#0;",7,[]string{"xmlParseCharRef: invalid xmlChar value 0"}},
   {"&#xD800;",11,[]string{"xmlParseCharRef: invalid xmlChar value 55296"}},
   {"&#x110000;",13,[]string{"xmlParseCharRef: character reference out of bounds"}},
   {"&#xZZ;",6,[]string{"CharRef: invalid hexadecimal value","xmlParseCharRef: invalid xmlChar value 0"}},
  } {s="<a>"+tc.ref+"</a>";e=reject(t,s,m);if e.Detail()!=blocks(1,s,tc.caret,tc.messages...){t.Fatalf("reference %s: %q",tc.ref,e.Detail())}}
  for _,tc:=range []struct{s,msg string;caret int}{{`<a><!--x--x--></a>`,"Double hyphen within comment: <!--x",8},{`<a><?XmL hi?></a>`,"Invalid PI name",8}} {e=reject(t,tc.s,m);if e.Detail()!=blocks(1,tc.s,tc.caret,tc.msg){t.Fatal(e.Detail())}}
  s=`<a x="<"/>`;e=reject(t,s,m)
  messages:=[]string{"Unescaped '<' not allowed in attributes values","attributes construct error","Couldn't find end of Start Tag a line 1"}
  expected:=blocks(1,s,6,messages...)
  if m==Content {expected+="\n"+blocks(1,s,7,"StartTag: invalid element name")}else{expected+="\n"+blocks(1,s,6,"Extra content at the end of the document")}
  if e.Detail()!=expected||len(e.Diagnostics)!=4{t.Fatalf("attribute recovery %d: %q",m,e.Detail())}
  // Source-derived CharData accelerator cursor, not a fresh SQL observation.
  s="<a>text]]></a>";e=reject(t,s,m)
  if e.Detail()!=blocks(1,s,3,"Sequence ']]>' not allowed in content")||e.Diagnostics[0].Offset!=3||e.Diagnostics[0].Column!=8{t.Fatal(e.Diagnostics)}
 }
 for _,tc:=range []struct{s,want string}{
  {"","line 1: switching encoding : no input\n\n^\nline 1: Document is empty\n\n^"},
  {"abc","line 1: Start tag expected, '<' not found\nabc\n^"},
  {"<a/><b/>","line 1: Extra content at the end of the document\n<a/><b/>\n    ^"},
 } {e:=reject(t,tc.s,Document);if e.Detail()!=tc.want{t.Fatalf("document %q",e.Detail())}}
 s:=`<?xml version="1.1"?><a>&#1;</a>`
 e:=reject(t,s,Content);if e.SourceBaseOffset!=21||e.Detail()!=blocks(1,"<a>&#1;</a>",7,"xmlParseCharRef: invalid xmlChar value 1"){t.Fatal(e.Detail(),e.SourceBaseOffset)}
 e=reject(t,s,Document);if len(e.Diagnostics)!=2||e.Diagnostics[0].Severity!=Warning||e.Detail()!=blocks(1,s,28,"xmlParseCharRef: invalid xmlChar value 1"){t.Fatal(e.Detail())}
}
func TestXMLCoreDiagnosticContexts(t *testing.T) {
 for _,m:=range []Mode{Content,Document} {
  s:="<é>\n  é&bad;\n</z>";e:=reject(t,s,m)
  want:="line 2: Entity 'bad' not defined\n  é&bad;\n         ^\nline 3: Opening and ending tag mismatch: é line 1 and z\n</z>\n    ^"
  if e.Detail()!=want||e.Diagnostics[0].Column!=9||e.Diagnostics[1].Column!=5 {t.Fatalf("multibyte %+v %q",e.Diagnostics,e.Detail())}
  for _,tc:=range []struct{s string;line int;source,caret string}{
   {"<a>\n\t\t&bad;\n</a>",2,"\t\t&bad;","\t\t     ^"},
   {"<a>\r\n\t&bad;\r\n</a>",2,"\t&bad;","\t     ^"},
   {"<a>\r\t&bad;\r</a>",1,"\t&bad;","\t     ^"},
   {"<a>\n\t漢é𐀀&bad;\n</a>",2,"\t漢é𐀀&bad;","\t              ^"},
   {"<a>"+strings.Repeat("x",100)+"&bad;</a>",1,strings.Repeat("x",75)+"&bad;",strings.Repeat(" ",79)+"^"},
   {"<a>"+strings.Repeat("x",100)+"&bad;"+strings.Repeat("y",100)+"</a>",1,strings.Repeat("x",75)+"&bad;",strings.Repeat(" ",79)+"^"},
   {"<a>&bad;"+strings.Repeat("y",100)+"</a>",1,"<a>&bad;"+strings.Repeat("y",72),"        ^"},
   // Bytea-hex oracle, not lossy JSON: the first byte really is isolated A9.
   {"<a>"+strings.Repeat("é",50)+"&bad;"+strings.Repeat("z",20)+"</a>",1,"\xa9"+strings.Repeat("é",37)+"&bad;",strings.Repeat(" ",79)+"^"},
  } {e=reject(t,tc.s,m);if len(e.Diagnostics)!=1 {t.Fatal(e.Diagnostics)};d:=e.Diagnostics[0];if d.Line!=tc.line||d.SourceContext!=tc.source||d.Caret!=tc.caret||d.Message!="Entity 'bad' not defined" {t.Fatalf("context %+v want source%q caret%q",d,tc.source,tc.caret)}}
 }
}

// Pinned PostgreSQL 18.6 UTF8/C captures: xml-core-review-remedy-evidence.md
// (independent scout, matching Debian libxml2 2.9.14 parser productions).
// The SQL oracle provides complete Detail literals; conditions and structured
// columns follow those productions, not the implementation under test.
func TestXMLCoreProductionDiagnostics(t *testing.T) {
 for _,m:=range []Mode{Content,Document} {
  for _,tc:=range []struct{
   name,input string
   offset,column int
   caret,detail string
   conditions []Condition
   messages []string
  }{
   {
    name:"comment",input:"<a><!--\x01--></a>",offset:7,column:8,caret:"       ^",
    detail:"line 1: xmlParseComment: invalid xmlChar value 1\n<a><!--\x01--></a>\n       ^\nline 1: PCDATA invalid Char value 1\n<a><!--\x01--></a>\n       ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR"},
    messages:[]string{"xmlParseComment: invalid xmlChar value 1","PCDATA invalid Char value 1"},
   },
   {
    name:"pi",input:"<a><?go \x01?></a>",offset:8,column:9,caret:"        ^",
    detail:"line 1: ParsePI: PI go never end ...\n<a><?go \x01?></a>\n        ^\nline 1: PCDATA invalid Char value 1\n<a><?go \x01?></a>\n        ^",
    conditions:[]Condition{"XML_ERR_PI_NOT_FINISHED","XML_ERR_INVALID_CHAR"},
    messages:[]string{"ParsePI: PI go never end ...","PCDATA invalid Char value 1"},
   },
   {
    name:"attribute",input:"<a x='\x01'/>",offset:6,column:7,caret:"      ^",
    detail:"line 1: invalid character in attribute value\n<a x='\x01'/>\n      ^\nline 1: attributes construct error\n<a x='\x01'/>\n      ^\nline 1: Couldn't find end of Start Tag a line 1\n<a x='\x01'/>\n      ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_SPACE_REQUIRED","XML_ERR_GT_REQUIRED"},
    messages:[]string{"invalid character in attribute value","attributes construct error","Couldn't find end of Start Tag a line 1"},
   },
  } {
   if tc.name=="attribute" {
    if m==Document {
     tc.detail+="\nline 1: Extra content at the end of the document\n<a x='\x01'/>\n      ^"
     tc.conditions=append(tc.conditions,"XML_ERR_DOCUMENT_END")
     tc.messages=append(tc.messages,"Extra content at the end of the document")
    }else{
     tc.detail+="\nline 1: PCDATA invalid Char value 1\n<a x='\x01'/>\n      ^"
     tc.conditions=append(tc.conditions,"XML_ERR_INVALID_CHAR")
     tc.messages=append(tc.messages,"PCDATA invalid Char value 1")
    }
   }
   e:=reject(t,tc.input,m) // Also asserts zero-result publication.
   primary:="invalid XML content";if m==Document {primary="invalid XML document"}
   if e.Error()!=primary||e.Category!=GrammarError||e.SourceBaseOffset!=0||e.Declaration!=(Declaration{})||e.Detail()!=tc.detail||len(e.Diagnostics)!=len(tc.conditions) {
    t.Fatalf("%s mode%d: %+v detail%q",tc.name,m,e,e.Detail())
   }
   for i,d:=range e.Diagnostics {
    want:=Diagnostic{Condition:tc.conditions[i],Domain:ParserDomain,Severity:Fatal,Line:1,Column:tc.column,Offset:tc.offset,Message:tc.messages[i],SourceContext:tc.input,Caret:tc.caret}
    if d!=want {t.Fatalf("%s mode%d event%d: %+v want %+v",tc.name,m,i,d,want)}
   }
   accept(t,"<healthy/>",m)
  }
 }
}

func TestXMLCoreCommentPreview(t *testing.T) {
 // Independent pinned literal: 60 payload bytes, but exactly 50 in the
 // diagnostic preview, excluding the separately printed four-byte opener.
 const input="<a><!--xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx--x--></a>"
 const message="Double hyphen within comment: <!--xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
 const detail="line 1: Double hyphen within comment: <!--xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n<a><!--xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx--x--></a>\n                                                                   ^"
 for _,m:=range []Mode{Content,Document} {
  e:=reject(t,input,m)
  want:=Diagnostic{Condition:"XML_ERR_HYPHEN_IN_COMMENT",Domain:ParserDomain,Severity:Fatal,Line:1,Column:68,Offset:67,Message:message,SourceContext:input,Caret:"                                                                   ^"}
  if len(e.Diagnostics)!=1||e.Diagnostics[0]!=want||e.Detail()!=detail {t.Fatalf("mode%d: %+v detail%q",m,e.Diagnostics,e.Detail())}
 }
}

func TestXMLCoreProductionCharacterBoundaries(t *testing.T) {
 // Independently captured binary xml_recv witnesses, not text-protocol NUL/FF.
 for _,m:=range []Mode{Content,Document} {
  for _,tc:=range []struct{
   input,detail,source,caret string
   offset,column int
   conditions []Condition
   messages []string
  }{
   {
    input:"<a><!--\x00--></a>",source:"<a><!--",caret:"       ^",offset:7,column:8,
    detail:"line 1: Comment not terminated\n<a><!--\n       ^\nline 1: Premature end of data in tag a line 1\n<a><!--\n       ^",
    conditions:[]Condition{"XML_ERR_COMMENT_NOT_FINISHED","XML_ERR_TAG_NOT_FINISHED"},
    messages:[]string{"Comment not terminated","Premature end of data in tag a line 1"},
   },
   {
    input:"<a x='\x00'/>",source:"<a x='",caret:"      ^",offset:6,column:7,
    detail:"line 1: AttValue: ' expected\n<a x='\n      ^\nline 1: attributes construct error\n<a x='\n      ^\nline 1: Couldn't find end of Start Tag a line 1\n<a x='\n      ^",
    conditions:[]Condition{"XML_ERR_ATTRIBUTE_NOT_FINISHED","XML_ERR_SPACE_REQUIRED","XML_ERR_GT_REQUIRED"},
    messages:[]string{"AttValue: ' expected","attributes construct error","Couldn't find end of Start Tag a line 1"},
   },
   {
    input:"<a><?go \x00?></a>",source:"<a><?go ",caret:"        ^",offset:8,column:9,
    detail:"line 1: ParsePI: PI go never end ...\n<a><?go \n        ^\nline 1: Premature end of data in tag a line 1\n<a><?go \n        ^",
    conditions:[]Condition{"XML_ERR_PI_NOT_FINISHED","XML_ERR_TAG_NOT_FINISHED"},
    messages:[]string{"ParsePI: PI go never end ...","Premature end of data in tag a line 1"},
   },
   {
    input:"<a><!--\xff--></a>",source:"<a><!--\xff--></a>",caret:"       ^",offset:7,column:8,
    detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x2D 0x2D 0x3E\n<a><!--\xff--></a>\n       ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR"},
    messages:[]string{"Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x2D 0x2D 0x3E"},
   },
   {
    input:"<a x='\xff'/>",source:"<a x='\xff'/>",caret:"      ^",offset:6,column:7,
    detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x27 0x2F 0x3E\n<a x='\xff'/>\n      ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR"},
    messages:[]string{"Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x27 0x2F 0x3E"},
   },
   {
    input:"<a><?go \xff?></a>",source:"<a><?go \xff?></a>",caret:"        ^",offset:8,column:9,
    detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x3F 0x3E 0x3C\n<a><?go \xff?></a>\n        ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR"},
    messages:[]string{"Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x3F 0x3E 0x3C"},
   },
  } {
   r,err:=Parse(context.Background(),tc.input,Options{Mode:m,InputKind:Binary})
   var e *ParseError
   if !errors.As(err,&e)||r.valid||r.original!=""||r.requested!=0||r.effective!=0||r.declaration!=(Declaration{})||len(r.diagnostics)!=0 {
    t.Fatalf("mode%d input%q: result%+v error%v",m,tc.input,r,err)
   }
   if e.RequestedMode!=m||e.EffectiveMode!=m||e.Category!=GrammarError||e.SourceBaseOffset!=0||e.Detail()!=tc.detail||len(e.Diagnostics)!=len(tc.conditions) {
    t.Fatalf("mode%d input%q: %+v detail%q",m,tc.input,e,e.Detail())
   }
   for i,d:=range e.Diagnostics {
    want:=Diagnostic{Condition:tc.conditions[i],Domain:ParserDomain,Severity:Fatal,Line:1,Column:tc.column,Offset:tc.offset,Message:tc.messages[i],SourceContext:tc.source,Caret:tc.caret}
    if d!=want {t.Fatalf("mode%d input%q event%d: %+v want %+v",m,tc.input,i,d,want)}
   }
   accept(t,"<healthy/>",m)
  }
 }
}

func TestXMLCoreComplexCommentDiagnostics(t *testing.T) {
 // Independent PostgreSQL 18.6 UTF8/C captures precede these literals:
 // xml-core-complex-comment-evidence.md. Structured fields follow the matching
 // libxml q/r/cur production, not output sampled from this parser.
 type event struct {
  condition Condition
  message string
  line,column,offset int
  source,caret string
 }
 for _,m:=range []Mode{Content,Document} {
  for _,tc:=range []struct{
   name,input,detail string
   events []event
  }{
   {
    name:"complex_hyphen",input:"<a><!--é--x--></a>",
    detail:"line 1: Comment must not contain '--' (double-hyphen)\n<a><!--é--x--></a>\n           ^",
    events:[]event{{"XML_ERR_HYPHEN_IN_COMMENT","Comment must not contain '--' (double-hyphen)",1,11,11,"<a><!--é--x--></a>","           ^"}},
   },
   {
    name:"invalid_second_lookahead",input:"<a><!--é\x01--></a>",
    detail:"line 1: xmlParseComment: invalid xmlChar value 233\n<a><!--é\x01--></a>\n         ^\nline 1: PCDATA invalid Char value 1\n<a><!--é\x01--></a>\n         ^",
    events:[]event{
     {"XML_ERR_INVALID_CHAR","xmlParseComment: invalid xmlChar value 233",1,9,9,"<a><!--é\x01--></a>","         ^"},
     {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 1",1,9,9,"<a><!--é\x01--></a>","         ^"},
    },
   },
   {
    name:"invalid_current_precedes_hyphen",input:"<a><!--é--\x01--></a>",
    detail:"line 1: xmlParseComment: invalid xmlChar value 1\n<a><!--é--\x01--></a>\n           ^\nline 1: PCDATA invalid Char value 1\n<a><!--é--\x01--></a>\n           ^",
    events:[]event{
     {"XML_ERR_INVALID_CHAR","xmlParseComment: invalid xmlChar value 1",1,11,11,"<a><!--é--\x01--></a>","           ^"},
     {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 1",1,11,11,"<a><!--é--\x01--></a>","           ^"},
    },
   },
   {
    name:"ascii_then_complex",input:"<a><!--a--xé--y--></a>",
    detail:"line 1: Double hyphen within comment: <!--a\n<a><!--a--xé--y--></a>\n        ^\nline 1: Comment must not contain '--' (double-hyphen)\n<a><!--a--xé--y--></a>\n               ^",
    events:[]event{
     {"XML_ERR_HYPHEN_IN_COMMENT","Double hyphen within comment: <!--a",1,9,8,"<a><!--a--xé--y--></a>","        ^"},
     {"XML_ERR_HYPHEN_IN_COMMENT","Comment must not contain '--' (double-hyphen)",1,15,15,"<a><!--a--xé--y--></a>","               ^"},
    },
   },
   {
    name:"lone_cr_transition",input:"<a><!--\r--x--></a>",
    detail:"line 1: Comment must not contain '--' (double-hyphen)\n--x--></a>\n  ^",
    events:[]event{{"XML_ERR_HYPHEN_IN_COMMENT","Comment must not contain '--' (double-hyphen)",1,11,10,"--x--></a>","  ^"}},
   },
   {
    name:"normalized_second_lookahead",input:"<a><!--\r\x01--></a>",
    detail:"line 1: xmlParseComment: invalid xmlChar value 10\n\x01--></a>\n^\nline 1: PCDATA invalid Char value 1\n\x01--></a>\n^",
    events:[]event{
     {"XML_ERR_INVALID_CHAR","xmlParseComment: invalid xmlChar value 10",1,9,8,"\x01--></a>","^"},
     {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 1",1,9,8,"\x01--></a>","^"},
    },
   },
   {
    name:"nul_initial_current",input:"<a><!--éx\x00--></a>",
    detail:"line 1: Comment not terminated\n<a><!--éx\n          ^\nline 1: Premature end of data in tag a line 1\n<a><!--éx\n          ^",
    events:[]event{
     {"XML_ERR_COMMENT_NOT_FINISHED","Comment not terminated",1,10,10,"<a><!--éx","          ^"},
     {"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag a line 1",1,10,10,"<a><!--éx","          ^"},
    },
   },
   {
    name:"nul_after_committed_payload",input:"<a><!--éxy\x00--></a>",
    detail:"line 1: Comment not terminated \n<!--é\n<a><!--éxy\n           ^\nline 1: Premature end of data in tag a line 1\n<a><!--éxy\n           ^",
    events:[]event{
     {"XML_ERR_COMMENT_NOT_FINISHED","Comment not terminated \n<!--é",1,11,11,"<a><!--éxy","           ^"},
     {"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag a line 1",1,11,11,"<a><!--éxy","           ^"},
    },
   },
   {
    name:"invalid_utf8_retained_hyphens",input:"<a><!--é--\xff--></a>",
    detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x2D 0x2D 0x3E\n<a><!--é--\xff--></a>\n           ^\nline 1: Comment must not contain '--' (double-hyphen)\n<a><!--é--\xff--></a>\n           ^",
    events:[]event{
     {"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x2D 0x2D 0x3E",1,11,11,"<a><!--é--\xff--></a>","           ^"},
     {"XML_ERR_HYPHEN_IN_COMMENT","Comment must not contain '--' (double-hyphen)",1,11,11,"<a><!--é--\xff--></a>","           ^"},
    },
   },
   {
    name:"invalid_first_lookahead",input:"<a><!--abc\x01--></a>",
    detail:"line 1: xmlParseComment: invalid xmlChar value 1\n<a><!--abc\x01--></a>\n          ^\nline 1: PCDATA invalid Char value 1\n<a><!--abc\x01--></a>\n          ^",
    events:[]event{
     {"XML_ERR_INVALID_CHAR","xmlParseComment: invalid xmlChar value 1",1,11,10,"<a><!--abc\x01--></a>","          ^"},
     {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 1",1,11,10,"<a><!--abc\x01--></a>","          ^"},
    },
   },
   {
    name:"invalid_initial_current",input:"<a><!--éx\x01--></a>",
    detail:"line 1: xmlParseComment: invalid xmlChar value 1\n<a><!--éx\x01--></a>\n          ^\nline 1: PCDATA invalid Char value 1\n<a><!--éx\x01--></a>\n          ^",
    events:[]event{
     {"XML_ERR_INVALID_CHAR","xmlParseComment: invalid xmlChar value 1",1,10,10,"<a><!--éx\x01--></a>","          ^"},
     {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 1",1,10,10,"<a><!--éx\x01--></a>","          ^"},
    },
   },
   {
    name:"multiple_complex_hyphens",input:"<a><!--é--x--y--></a>",
    detail:"line 1: Comment must not contain '--' (double-hyphen)\n<a><!--é--x--y--></a>\n           ^\nline 1: Comment must not contain '--' (double-hyphen)\n<a><!--é--x--y--></a>\n              ^",
    events:[]event{
     {"XML_ERR_HYPHEN_IN_COMMENT","Comment must not contain '--' (double-hyphen)",1,11,11,"<a><!--é--x--y--></a>","           ^"},
     {"XML_ERR_HYPHEN_IN_COMMENT","Comment must not contain '--' (double-hyphen)",1,14,14,"<a><!--é--x--y--></a>","              ^"},
    },
   },
   {
    name:"crlf_stays_ascii",input:"<a><!--\r\n--x--></a>",
    detail:"line 2: Double hyphen within comment: <!--\n--x--></a>\n^",
    events:[]event{{"XML_ERR_HYPHEN_IN_COMMENT","Double hyphen within comment: <!--",2,1,9,"--x--></a>","^"}},
   },
   {
    name:"crlf_does_not_leave_complex",input:"<a><!--é\r\n--x--></a>",
    detail:"line 2: Comment must not contain '--' (double-hyphen)\n--x--></a>\n  ^",
    events:[]event{{"XML_ERR_HYPHEN_IN_COMMENT","Comment must not contain '--' (double-hyphen)",2,3,13,"--x--></a>","  ^"}},
   },
   {
    name:"nul_first_lookahead",input:"<a><!--abc\x00--></a>",
    detail:"line 1: Comment not terminated\n<a><!--abc\n          ^\nline 1: Premature end of data in tag a line 1\n<a><!--abc\n          ^",
    events:[]event{
     {"XML_ERR_COMMENT_NOT_FINISHED","Comment not terminated",1,11,10,"<a><!--abc","          ^"},
     {"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag a line 1",1,11,10,"<a><!--abc","          ^"},
    },
   },
   {
    name:"nul_second_lookahead",input:"<a><!--é\x00--></a>",
    detail:"line 1: Comment not terminated\n<a><!--é\n         ^\nline 1: Premature end of data in tag a line 1\n<a><!--é\n         ^",
    events:[]event{
     {"XML_ERR_COMMENT_NOT_FINISHED","Comment not terminated",1,9,9,"<a><!--é","         ^"},
     {"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag a line 1",1,9,9,"<a><!--é","         ^"},
    },
   },
   {
    name:"invalid_utf8_first_lookahead",input:"<a><!--abc\xff--></a>",
    detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x2D 0x2D 0x3E\n<a><!--abc\xff--></a>\n          ^",
    events:[]event{{"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x2D 0x2D 0x3E",1,11,10,"<a><!--abc\xff--></a>","          ^"}},
   },
   {
    name:"invalid_utf8_second_lookahead",input:"<a><!--é\xff--></a>",
    detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x2D 0x2D 0x3E\n<a><!--é\xff--></a>\n         ^",
    events:[]event{{"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x2D 0x2D 0x3E",1,9,9,"<a><!--é\xff--></a>","         ^"}},
   },
   {
    name:"invalid_utf8_initial_current",input:"<a><!--éx\xff--></a>",
    detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x2D 0x2D 0x3E\n<a><!--éx\xff--></a>\n          ^",
    events:[]event{{"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x2D 0x2D 0x3E",1,10,10,"<a><!--éx\xff--></a>","          ^"}},
   },
   {
    name:"invalid_utf8_later_current",input:"<a><!--éxy\xff--></a>",
    detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x2D 0x2D 0x3E\n<a><!--éxy\xff--></a>\n           ^",
    events:[]event{{"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x2D 0x2D 0x3E",1,11,11,"<a><!--éxy\xff--></a>","           ^"}},
   },
   {
    name:"overlapping_complex_hyphens",input:"<a><!--é----></a>",
    detail:"line 1: Comment must not contain '--' (double-hyphen)\n<a><!--é----></a>\n           ^\nline 1: Comment must not contain '--' (double-hyphen)\n<a><!--é----></a>\n            ^",
    events:[]event{
     {"XML_ERR_HYPHEN_IN_COMMENT","Comment must not contain '--' (double-hyphen)",1,11,11,"<a><!--é----></a>","           ^"},
     {"XML_ERR_HYPHEN_IN_COMMENT","Comment must not contain '--' (double-hyphen)",1,12,12,"<a><!--é----></a>","            ^"},
    },
   },
   {
    name:"overlapping_ascii_hyphens",input:"<a><!--a----></a>",
    detail:"line 1: Double hyphen within comment: <!--a\n<a><!--a----></a>\n        ^",
    events:[]event{{"XML_ERR_HYPHEN_IN_COMMENT","Double hyphen within comment: <!--a",1,9,8,"<a><!--a----></a>","        ^"}},
   },
   {
    name:"invalid_later_current",input:"<a><!--éxy\x01--></a>",
    detail:"line 1: xmlParseComment: invalid xmlChar value 1\n<a><!--éxy\x01--></a>\n           ^\nline 1: PCDATA invalid Char value 1\n<a><!--éxy\x01--></a>\n           ^",
    events:[]event{
     {"XML_ERR_INVALID_CHAR","xmlParseComment: invalid xmlChar value 1",1,11,11,"<a><!--éxy\x01--></a>","           ^"},
     {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 1",1,11,11,"<a><!--éxy\x01--></a>","           ^"},
    },
   },
   {
    name:"ascii_prefix_complex_transition",input:"<a><!--abcé--x--></a>",
    detail:"line 1: Comment must not contain '--' (double-hyphen)\n<a><!--abcé--x--></a>\n              ^",
    events:[]event{{"XML_ERR_HYPHEN_IN_COMMENT","Comment must not contain '--' (double-hyphen)",1,14,14,"<a><!--abcé--x--></a>","              ^"}},
   },
   {
    name:"nul_retained_hyphens",input:"<a><!--é--\x00--></a>",
    detail:"line 1: Comment not terminated \n<!--é\n<a><!--é--\n           ^\nline 1: Premature end of data in tag a line 1\n<a><!--é--\n           ^",
    events:[]event{
     {"XML_ERR_COMMENT_NOT_FINISHED","Comment not terminated \n<!--é",1,11,11,"<a><!--é--","           ^"},
     {"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag a line 1",1,11,11,"<a><!--é--","           ^"},
    },
   },
  } {
   t.Run(tc.name+map[Mode]string{Content:"/content",Document:"/document"}[m],func(t *testing.T){
    r,err:=Parse(context.Background(),tc.input,Options{Mode:m,InputKind:Binary})
    var e *ParseError
    if !errors.As(err,&e)||r.valid||r.original!=""||r.requested!=0||r.effective!=0||r.declaration!=(Declaration{})||len(r.diagnostics)!=0 {
     t.Fatalf("result%+v error%v",r,err)
    }
    primary:="invalid XML content";if m==Document {primary="invalid XML document"}
    if e.Error()!=primary||e.RequestedMode!=m||e.EffectiveMode!=m||e.Category!=GrammarError||e.SourceBaseOffset!=0||e.Declaration!=(Declaration{})||e.Detail()!=tc.detail||len(e.Diagnostics)!=len(tc.events) {
     t.Fatalf("%+v detail%q want%q",e,e.Detail(),tc.detail)
    }
    for i,v:=range tc.events {
     want:=Diagnostic{Condition:v.condition,Domain:ParserDomain,Severity:Fatal,Line:v.line,Column:v.column,Offset:v.offset,Message:v.message,SourceContext:v.source,Caret:v.caret}
     if e.Diagnostics[i]!=want {t.Fatalf("event%d: %+v want %+v",i,e.Diagnostics[i],want)}
    }
    accept(t,"<healthy/>",m)
   })
  }
  for _,s:=range []string{"<a><!--é--></a>","<a><!--abcé-x--></a>","<a><!--\r--></a>","<a><!--\r\n--></a>","<a><!--é\r\n--></a>"} {
   r:=accept(t,s,m)
   if len(r.Diagnostics())!=0 {t.Fatalf("valid comment %q: %+v",s,r.Diagnostics())}
  }
 }
}

func TestXMLCoreDecoderRangeDiagnostics(t *testing.T) {
 // Independent pinned PostgreSQL18.6 UTF8/C captures precede these literals:
 // xml-core-decoder-range-evidence.md. Conditions, offsets and character columns
 // follow matching Debian libxml2; SQL exposes Detail, not those fields.
 for _,m:=range []Mode{Content,Document} {
  for _,tc:=range []struct{
   name,input,detail,caret string
   offset,column int
   conditions []Condition
   messages []string
  }{
   {
    name:"text",input:"<a>\uFFFE</a>",offset:3,column:4,caret:"   ^",
    detail:"line 1: Char 0xFFFE out of allowed range\n<a>\uFFFE</a>\n   ^\nline 1: PCDATA invalid Char value 65534\n<a>\uFFFE</a>\n   ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR"},
    messages:[]string{"Char 0xFFFE out of allowed range","PCDATA invalid Char value 65534"},
   },
   {
    name:"comment_first",input:"<a><!--\uFFFE--></a>",offset:7,column:8,caret:"       ^",
    detail:"line 1: Char 0xFFFE out of allowed range\n<a><!--\uFFFE--></a>\n       ^\nline 1: xmlParseComment: invalid xmlChar value 65534\n<a><!--\uFFFE--></a>\n       ^\nline 1: Char 0xFFFE out of allowed range\n<a><!--\uFFFE--></a>\n       ^\nline 1: PCDATA invalid Char value 65534\n<a><!--\uFFFE--></a>\n       ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR"},
    messages:[]string{"Char 0xFFFE out of allowed range","xmlParseComment: invalid xmlChar value 65534","Char 0xFFFE out of allowed range","PCDATA invalid Char value 65534"},
   },
   {
    name:"comment_second",input:"<a><!--é\uFFFE--></a>",offset:9,column:9,caret:"         ^",
    detail:"line 1: Char 0xFFFE out of allowed range\n<a><!--é\uFFFE--></a>\n         ^\nline 1: xmlParseComment: invalid xmlChar value 233\n<a><!--é\uFFFE--></a>\n         ^\nline 1: Char 0xFFFE out of allowed range\n<a><!--é\uFFFE--></a>\n         ^\nline 1: PCDATA invalid Char value 65534\n<a><!--é\uFFFE--></a>\n         ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR"},
    messages:[]string{"Char 0xFFFE out of allowed range","xmlParseComment: invalid xmlChar value 233","Char 0xFFFE out of allowed range","PCDATA invalid Char value 65534"},
   },
   {
    name:"comment_initial_current",input:"<a><!--éx\uFFFE--></a>",offset:10,column:10,caret:"          ^",
    detail:"line 1: Char 0xFFFE out of allowed range\n<a><!--éx\uFFFE--></a>\n          ^\nline 1: xmlParseComment: invalid xmlChar value 65534\n<a><!--éx\uFFFE--></a>\n          ^\nline 1: Char 0xFFFE out of allowed range\n<a><!--éx\uFFFE--></a>\n          ^\nline 1: PCDATA invalid Char value 65534\n<a><!--éx\uFFFE--></a>\n          ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR"},
    messages:[]string{"Char 0xFFFE out of allowed range","xmlParseComment: invalid xmlChar value 65534","Char 0xFFFE out of allowed range","PCDATA invalid Char value 65534"},
   },
   {
    name:"comment_rolling_current",input:"<a><!--éxy\uFFFE--></a>",offset:11,column:11,caret:"           ^",
    detail:"line 1: Char 0xFFFE out of allowed range\n<a><!--éxy\uFFFE--></a>\n           ^\nline 1: xmlParseComment: invalid xmlChar value 65534\n<a><!--éxy\uFFFE--></a>\n           ^\nline 1: Char 0xFFFE out of allowed range\n<a><!--éxy\uFFFE--></a>\n           ^\nline 1: PCDATA invalid Char value 65534\n<a><!--éxy\uFFFE--></a>\n           ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR"},
    messages:[]string{"Char 0xFFFE out of allowed range","xmlParseComment: invalid xmlChar value 65534","Char 0xFFFE out of allowed range","PCDATA invalid Char value 65534"},
   },
   {
    name:"comment_retained_hyphens",input:"<a><!--é--\uFFFE--></a>",offset:11,column:11,caret:"           ^",
    detail:"line 1: Char 0xFFFE out of allowed range\n<a><!--é--\uFFFE--></a>\n           ^\nline 1: xmlParseComment: invalid xmlChar value 65534\n<a><!--é--\uFFFE--></a>\n           ^\nline 1: Char 0xFFFE out of allowed range\n<a><!--é--\uFFFE--></a>\n           ^\nline 1: PCDATA invalid Char value 65534\n<a><!--é--\uFFFE--></a>\n           ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR"},
    messages:[]string{"Char 0xFFFE out of allowed range","xmlParseComment: invalid xmlChar value 65534","Char 0xFFFE out of allowed range","PCDATA invalid Char value 65534"},
   },
   {
    name:"pi",input:"<a><?go \uFFFE?></a>",offset:8,column:9,caret:"        ^",
    detail:"line 1: Char 0xFFFE out of allowed range\n<a><?go \uFFFE?></a>\n        ^\nline 1: ParsePI: PI go never end ...\n<a><?go \uFFFE?></a>\n        ^\nline 1: Char 0xFFFE out of allowed range\n<a><?go \uFFFE?></a>\n        ^\nline 1: PCDATA invalid Char value 65534\n<a><?go \uFFFE?></a>\n        ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_PI_NOT_FINISHED","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR"},
    messages:[]string{"Char 0xFFFE out of allowed range","ParsePI: PI go never end ...","Char 0xFFFE out of allowed range","PCDATA invalid Char value 65534"},
   },
   {
    name:"attribute",input:"<a x='\uFFFE'/>",offset:6,column:7,caret:"      ^",
    detail:"line 1: Char 0xFFFE out of allowed range\n<a x='\uFFFE'/>\n      ^\nline 1: invalid character in attribute value\n<a x='\uFFFE'/>\n      ^\nline 1: attributes construct error\n<a x='\uFFFE'/>\n      ^\nline 1: Couldn't find end of Start Tag a line 1\n<a x='\uFFFE'/>\n      ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_SPACE_REQUIRED","XML_ERR_GT_REQUIRED"},
    messages:[]string{"Char 0xFFFE out of allowed range","invalid character in attribute value","attributes construct error","Couldn't find end of Start Tag a line 1"},
   },
   {
    name:"text_ffff",input:"<a>\uFFFF</a>",offset:3,column:4,caret:"   ^",
    detail:"line 1: Char 0xFFFF out of allowed range\n<a>\uFFFF</a>\n   ^\nline 1: PCDATA invalid Char value 65535\n<a>\uFFFF</a>\n   ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR"},
    messages:[]string{"Char 0xFFFF out of allowed range","PCDATA invalid Char value 65535"},
   },
   {
    name:"comment_ffff",input:"<a><!--\uFFFF--></a>",offset:7,column:8,caret:"       ^",
    detail:"line 1: Char 0xFFFF out of allowed range\n<a><!--\uFFFF--></a>\n       ^\nline 1: xmlParseComment: invalid xmlChar value 65535\n<a><!--\uFFFF--></a>\n       ^\nline 1: Char 0xFFFF out of allowed range\n<a><!--\uFFFF--></a>\n       ^\nline 1: PCDATA invalid Char value 65535\n<a><!--\uFFFF--></a>\n       ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR"},
    messages:[]string{"Char 0xFFFF out of allowed range","xmlParseComment: invalid xmlChar value 65535","Char 0xFFFF out of allowed range","PCDATA invalid Char value 65535"},
   },
   {
    name:"pi_ffff",input:"<a><?go \uFFFF?></a>",offset:8,column:9,caret:"        ^",
    detail:"line 1: Char 0xFFFF out of allowed range\n<a><?go \uFFFF?></a>\n        ^\nline 1: ParsePI: PI go never end ...\n<a><?go \uFFFF?></a>\n        ^\nline 1: Char 0xFFFF out of allowed range\n<a><?go \uFFFF?></a>\n        ^\nline 1: PCDATA invalid Char value 65535\n<a><?go \uFFFF?></a>\n        ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_PI_NOT_FINISHED","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR"},
    messages:[]string{"Char 0xFFFF out of allowed range","ParsePI: PI go never end ...","Char 0xFFFF out of allowed range","PCDATA invalid Char value 65535"},
   },
   {
    name:"attribute_ffff",input:"<a x='\uFFFF'/>",offset:6,column:7,caret:"      ^",
    detail:"line 1: Char 0xFFFF out of allowed range\n<a x='\uFFFF'/>\n      ^\nline 1: invalid character in attribute value\n<a x='\uFFFF'/>\n      ^\nline 1: attributes construct error\n<a x='\uFFFF'/>\n      ^\nline 1: Couldn't find end of Start Tag a line 1\n<a x='\uFFFF'/>\n      ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_SPACE_REQUIRED","XML_ERR_GT_REQUIRED"},
    messages:[]string{"Char 0xFFFF out of allowed range","invalid character in attribute value","attributes construct error","Couldn't find end of Start Tag a line 1"},
   },
   {
    name:"cdata_initial",input:"<a><![CDATA[\uFFFE]]></a>",offset:12,column:13,caret:"            ^",
    detail:"line 1: Char 0xFFFE out of allowed range\n<a><![CDATA[\uFFFE]]></a>\n            ^\nline 1: Unregistered error message\n<a><![CDATA[\uFFFE]]></a>\n            ^\nline 1: Char 0xFFFE out of allowed range\n<a><![CDATA[\uFFFE]]></a>\n            ^\nline 1: PCDATA invalid Char value 65534\n<a><![CDATA[\uFFFE]]></a>\n            ^\nline 1: Sequence ']]>' not allowed in content\n<a><![CDATA[\uFFFE]]></a>\n               ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_CDATA_NOT_FINISHED","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_MISPLACED_CDATA_END"},
    messages:[]string{"Char 0xFFFE out of allowed range","Unregistered error message","Char 0xFFFE out of allowed range","PCDATA invalid Char value 65534","Sequence ']]>' not allowed in content"},
   },
   {
    name:"cdata_initial_ffff",input:"<a><![CDATA[\uFFFF]]></a>",offset:12,column:13,caret:"            ^",
    detail:"line 1: Char 0xFFFF out of allowed range\n<a><![CDATA[\uFFFF]]></a>\n            ^\nline 1: Unregistered error message\n<a><![CDATA[\uFFFF]]></a>\n            ^\nline 1: Char 0xFFFF out of allowed range\n<a><![CDATA[\uFFFF]]></a>\n            ^\nline 1: PCDATA invalid Char value 65535\n<a><![CDATA[\uFFFF]]></a>\n            ^\nline 1: Sequence ']]>' not allowed in content\n<a><![CDATA[\uFFFF]]></a>\n               ^",
    conditions:[]Condition{"XML_ERR_INVALID_CHAR","XML_ERR_CDATA_NOT_FINISHED","XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR","XML_ERR_MISPLACED_CDATA_END"},
    messages:[]string{"Char 0xFFFF out of allowed range","Unregistered error message","Char 0xFFFF out of allowed range","PCDATA invalid Char value 65535","Sequence ']]>' not allowed in content"},
   },
  } {
   if tc.name=="attribute"||tc.name=="attribute_ffff" {
    if m==Document {
     tc.conditions=append(tc.conditions,"XML_ERR_DOCUMENT_END")
     tc.messages=append(tc.messages,"Extra content at the end of the document")
     tc.detail+="\nline 1: Extra content at the end of the document\n"+tc.input+"\n      ^"
    }else if tc.name=="attribute" {
     tc.conditions=append(tc.conditions,"XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR")
     tc.messages=append(tc.messages,"Char 0xFFFE out of allowed range","PCDATA invalid Char value 65534")
     tc.detail+="\nline 1: Char 0xFFFE out of allowed range\n<a x='\uFFFE'/>\n      ^\nline 1: PCDATA invalid Char value 65534\n<a x='\uFFFE'/>\n      ^"
    }else{
     tc.conditions=append(tc.conditions,"XML_ERR_INVALID_CHAR","XML_ERR_INVALID_CHAR")
     tc.messages=append(tc.messages,"Char 0xFFFF out of allowed range","PCDATA invalid Char value 65535")
     tc.detail+="\nline 1: Char 0xFFFF out of allowed range\n<a x='\uFFFF'/>\n      ^\nline 1: PCDATA invalid Char value 65535\n<a x='\uFFFF'/>\n      ^"
    }
   }
   for _,kind:=range []InputKind{Text,Binary} {
    t.Run(tc.name+map[Mode]string{Content:"/content",Document:"/document"}[m]+map[InputKind]string{Text:"/text",Binary:"/binary"}[kind],func(t *testing.T){
     r,err:=Parse(context.Background(),tc.input,Options{Mode:m,InputKind:kind})
     var e *ParseError
     if !errors.As(err,&e)||r.valid||r.original!=""||r.requested!=0||r.effective!=0||r.declaration!=(Declaration{})||len(r.diagnostics)!=0 {
      t.Fatalf("result%+v error%v",r,err)
     }
     primary:="invalid XML content";if m==Document {primary="invalid XML document"}
     if e.Error()!=primary||e.RequestedMode!=m||e.EffectiveMode!=m||e.Category!=GrammarError||e.SourceBaseOffset!=0||e.Declaration!=(Declaration{})||e.Detail()!=tc.detail||len(e.Diagnostics)!=len(tc.conditions) {
      t.Fatalf("%+v detail%q want%q",e,e.Detail(),tc.detail)
     }
     for i,message:=range tc.messages {
      want:=Diagnostic{Condition:tc.conditions[i],Domain:ParserDomain,Severity:Fatal,Line:1,Column:tc.column,Offset:tc.offset,Message:message,SourceContext:tc.input,Caret:tc.caret}
      if tc.conditions[i]=="XML_ERR_MISPLACED_CDATA_END" {
       want.Offset=15;want.Column=14;want.Caret="               ^"
      }
      if e.Diagnostics[i]!=want {t.Fatalf("event%d: %+v want %+v",i,e.Diagnostics[i],want)}
     }
     accept(t,"<healthy/>",m)
    })
   }
  }
 }
}
