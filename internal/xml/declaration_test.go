package xml

import (
 "context"
 "strings"
 "testing"
)

func blocks(line int,source string,caret int,messages ...string) string {
 out:=make([]string,0,len(messages))
 for _,message:=range messages {out=append(out,Diagnostic{Line:line,Message:message,SourceContext:source,Caret:strings.Repeat(" ",caret)+"^"}.String())}
 return strings.Join(out,"\n")
}
func TestXMLCoreDeclarations(t *testing.T) {
 for _,version:=range []string{"1.0","1.1","2.0",""} {
  s:=`<?xml version="`+version+`"?><a/>`
  r:=accept(t,s,Content)
  if d:=r.Declaration();!d.Present||d.Version!=version||d.End!=len(s)-4||d.EncodingPresent||d.Standalone!=Absent {t.Fatal(d)}
  if version=="1.0"||version=="1.1" {r=accept(t,s,Document);if version=="1.1" {
   want:=Diagnostic{Condition:"XML_WAR_UNKNOWN_VERSION",Domain:ParserDomain,Severity:Warning,Line:1,Column:20,Offset:19,Message:"Unsupported version '1.1'",SourceContext:s,Caret:strings.Repeat(" ",19)+"^"}
   ds:=r.Diagnostics();if len(ds)!=1||ds[0]!=want {t.Fatalf("warning %+v want %+v",ds,want)}
  }}
 }
 // Source-derived matching parser.c VersionNum and forward-compatible1.*.
 for _,version:=range []string{"1.","1.00","1.2","1.234"} {
  r:=accept(t,`<?xml version="`+version+`"?><a/>`,Document)
  ds:=r.Diagnostics()
  if len(ds)!=1||ds[0].Severity!=Warning||ds[0].Message!="Unsupported version '"+version+"'" {t.Fatal(ds)}
 }
 for _,label:=range []string{"BOGUS","","1X","UTF-16"} {
  s:=`<?xml version="1.0" encoding="`+label+`"?><a/>`;r:=accept(t,s,Content)
  if d:=r.Declaration();!d.EncodingPresent||d.Encoding!=label {t.Fatal(d)}
 }
 custom:=[]struct{s,detail string}{
  {`<?xml`,"Space required."},{`<?xml?><a/>`,"Space required."},
  {`<?xml encoding="UTF8"?><a/>`,"Malformed declaration: missing version."},
  {`<?xml version=1.0?><a/>`,"Malformed declaration: missing version."},
  {`<?xml version="1.0?><a/>`,"Malformed declaration: missing version."},
  {`<?xml version="1.0"encoding="UTF8"?><a/>`,"Space required."},
  {`<?xml version="1.0" standalone="y"?><a/>`,"standalone accepts only 'yes' or 'no'."},
  {`<?xml version="1.0" encoding=UTF8?><a/>`,"Missing encoding in text declaration."},
  {`<?xml version="1.0" standalone="yes" encoding="UTF8"?><a/>`,"Parsing XML declaration: '?>' expected."},
  {`<?xml version="1.0" unknown="x"?><a/>`,"Parsing XML declaration: '?>' expected."},
  {`<?xml version="é"?><a/>`,"Invalid character value."},
 }
 for _,tc:=range custom {e:=reject(t,tc.s,Content);if e.Category!=DeclarationError||e.Error()!="invalid XML content: invalid XML declaration"||e.Detail()!=tc.detail||len(e.Diagnostics)!=0 {t.Fatalf("custom %q: %+v detail %q",tc.s,e,e.Detail())}}
 doc:=[]struct{s string;caret int;messages []string}{
  {`<?xml version="2.0"?><a/>`,19,[]string{"Unsupported version '2.0'"}},
  {`<?xml version=""?><a/>`,16,[]string{"Malformed declaration expecting version"}},
  {`<?xml version="1.0" encoding="BOGUS"?><a/>`,36,[]string{"Unsupported encoding BOGUS"}},
  {`<?xml version="1.0" encoding=""?><a/>`,30,[]string{"Invalid XML encoding name"}},
  {`<?xml version="1.0" encoding="1X"?><a/>`,30,[]string{"Invalid XML encoding name","String not closed expecting \" or '","parsing XML declaration: '?>' expected"}},
  {`<?xml`,5,[]string{"XML declaration allowed only at the start of the document","ParsePI: PI xml space expected","ParsePI: PI xml never end ...","Start tag expected, '<' not found"}},
  {`<?xml?><a/>`,5,[]string{"XML declaration allowed only at the start of the document"}},
  {`<?xml encoding="UTF8"?><a/>`,6,[]string{"Malformed declaration expecting version","Blank needed here"}},
  {`<?xml version=1.0?><a/>`,14,[]string{"String not started expecting ' or \"","Malformed declaration expecting version","Blank needed here","parsing XML declaration: '?>' expected"}},
  {`<?xml version="1.0?><a/>`,18,[]string{"String not closed expecting \" or '"}},
  {`<?xml version="1.0"encoding="UTF8"?><a/>`,19,[]string{"Blank needed here"}},
  {`<?xml version="1.0" standalone="y"?><a/>`,32,[]string{"standalone accepts only 'yes' or 'no'","String not closed expecting \" or '","parsing XML declaration: '?>' expected"}},
  {`<?xml version="1.0" standalone="yes" encoding="UTF8"?><a/>`,37,[]string{"parsing XML declaration: '?>' expected"}},
  {`<?xml version="1.0" unknown="x"?><a/>`,20,[]string{"parsing XML declaration: '?>' expected"}},
  {`<?xml version="é"?><a/>`,15,[]string{"String not closed expecting \" or '","Malformed declaration expecting version","Blank needed here","parsing XML declaration: '?>' expected"}},
 }
 for _,tc:=range doc {e:=reject(t,tc.s,Document);want:=blocks(1,tc.s,tc.caret,tc.messages...);if e.Detail()!=want||e.Error()!="invalid XML document" {t.Fatalf("document %q\ngot %q\nwant %q",tc.s,e.Detail(),want)}}
 s:="<?xml\r\nversion=\"1.0\"\r\nencoding=UTF8?><a/>"
 e:=reject(t,s,Document);want:=blocks(3,"encoding=UTF8?><a/>",9,"String not started expecting ' or \"","parsing XML declaration: '?>' expected");if e.Detail()!=want{t.Fatalf("CRLF declaration %q",e.Detail())}
 for _,tc:=range []struct{s string;caret int}{{` <?xml version="1.0"?><a/>`,6},{`<a/><?xml version="1.0"?>`,9}} {
  for _,m:=range []Mode{Content,Document} {e:=reject(t,tc.s,m);if e.Detail()!=blocks(1,tc.s,tc.caret,"XML declaration allowed only at the start of the document") {t.Fatal(e.Detail())}}
 }
 for _,m:=range []Mode{Content,Document} {accept(t,`<?xml-stylesheet x?><a/>`,m);reject(t,`<?xml version="1.1"?><a>&#1;</a>`,m)}
 r:=accept(t,`<?xml version='1.0' encoding='' standalone='no'?><a/>`,Content)
 if d:=r.Declaration();d.Standalone!=No||!d.EncodingPresent||d.Encoding!="" {t.Fatal(d)}
 _,_,err:=readContentDeclaration(context.Background(),`<?xml version='1.0'?>`);if err!=nil {t.Fatal(err)}
}
