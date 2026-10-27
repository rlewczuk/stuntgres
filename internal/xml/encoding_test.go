package xml

import (
 "context"
 "errors"
 "strings"
 "testing"
)

func TestXMLCoreEncoding(t *testing.T) {
 ctx:=context.Background()
 header:=`<?xml version="1.0" encoding="ISO-8859-1"?>`
 for _,mode:=range []Mode{Content,Document} {
  s:=header+"<a>é</a>";accept(t,s,mode)
  r,e:=Parse(ctx,header+"<a>\xe9</a>",Options{Mode:mode,InputKind:Binary})
  if e!=nil||r.Original()!=s||r.Declaration().Encoding!="ISO-8859-1" {t.Fatal(r,e)}
  r,e=Parse(ctx,"<a>é</a>",Options{Mode:mode,InputKind:Binary});if e!=nil||r.Original()!="<a>é</a>"{t.Fatal(r,e)}
  r,e=Parse(ctx,"<a>\xe9</a>",Options{Mode:mode,InputKind:Binary});var pe *ParseError
  if !errors.As(e,&pe)||r.valid||pe.Detail()!="line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xE9 0x3C 0x2F 0x61\n<a>\xe9</a>\n   ^" {t.Fatalf("default binary UTF8 %v %+v",e,pe)}
  for _,label:=range []string{"UTF-16","BOGUS",""} {
   r,e=Parse(ctx,`<?xml version="1.0" encoding="`+label+`"?><a/>`,Options{Mode:mode,InputKind:Binary});var ee *EncodingError
   if !errors.As(e,&ee)||r.valid||ee.Kind!=InvalidEncodingName||ee.Error()!=`invalid encoding name "`+label+`"`{t.Fatalf("label %q %+v",label,ee)}
  }
  for _,body:=range []string{"<a/>","<a>\x81\xa1</a>"} {
   r,e=Parse(ctx,`<?xml version="1.0" encoding="MULE_INTERNAL"?>`+body,Options{Mode:mode,InputKind:Binary});var ee *EncodingError
   if !errors.As(e,&ee)||ee.Kind!=MissingConversion||ee.SourceID!=7||ee.DestinationID!=6||ee.Message!=`default conversion function for encoding "MULE_INTERNAL" to "UTF8" does not exist`||ee.Detail!=""||r.valid {t.Fatalf("MULE precedence %+v",ee)}
  }
  r,e=Parse(ctx,`<?xml version="1.0" encoding="JOHAB"?><a/>`,Options{Mode:mode,InputKind:Binary});if e!=nil||!r.valid{t.Fatal(r,e)}
  _,e=Parse(ctx,"<?xml version=\"1.0\" encoding=\"JOHAB\"?><a>\x84\x41</a>",Options{Mode:mode,InputKind:Binary});var ee *EncodingError
  if !errors.As(e,&ee)||ee.Kind!=InvalidSequence||ee.Message!=`invalid byte sequence for encoding "JOHAB": 0x84 0x41`{t.Fatalf("JOHAB %+v",ee)}
  s=`<?xml version="1.0" encoding="LATIN1" standalone="y"?><a>é</a>`
  _,e=Parse(ctx,strings.Replace(s,"é","\xe9",1),Options{Mode:mode,InputKind:Binary})
  if !errors.As(e,&pe){t.Fatal(e)}
  if mode==Content {if pe.Category!=DeclarationError||pe.Detail()!="standalone accepts only 'yes' or 'no'."{t.Fatal(pe)}}else if pe.Detail()!=blocks(1,s,50,"standalone accepts only 'yes' or 'no'","String not closed expecting \" or '","parsing XML declaration: '?>' expected"){t.Fatal(pe.Detail())}
  _,e=Parse(ctx,strings.Replace(s,"LATIN1","MULE_INTERNAL",1),Options{Mode:mode,InputKind:Binary})
  if !errors.As(e,&ee)||ee.Kind!=MissingConversion {t.Fatalf("partial declaration precedence %v",e)}
 }
 // Source-derived partial metadata still resolves MULE before the completed
 // custom declaration's nonASCII check (xml.c1433-1565).
 _,e:=Parse(ctx,`<?xml version="é" encoding="MULE_INTERNAL"?><a/>`,Options{InputKind:Binary})
 var ee *EncodingError
 if !errors.As(e,&ee)||ee.Kind!=MissingConversion {t.Fatalf("late ASCII preflight %v",e)}
 // Reuse both packed output words and combined lookahead, not just one scalar.
 r,e:=Parse(ctx,"<?xml version='1.0' encoding='EUC_JIS_2004'?><a>\xa4\xf7</a>",Options{InputKind:Binary})
 if e!=nil||r.Original()!="<?xml version='1.0' encoding='EUC_JIS_2004'?><a>か゚</a>"{t.Fatal(r,e)}
 out,w,e:=OutputBinary(ctx,r,5)
 if e!=nil||w!=(OutputWarning{})||out!="<?xml version=\"1.0\" encoding=\"EUC_JIS_2004\"?><a>\xa4\xf7</a>"{t.Fatal(out,w,e)}
 _,e=convert(ctx,"漢\xff",6,8)
 if !errors.As(e,&ee)||ee.Kind!=InvalidSequence {t.Fatalf("whole-source validation before Step %v",e)}
 _,e=Parse(ctx,"<?xml version='1.0' encoding='SQL_ASCII'?><a>\xff</a>",Options{InputKind:Binary})
 var malformed *ParseError
 if !errors.As(e,&malformed) {t.Fatalf("SQL_ASCII must still be XML UTF8: %v",e)}
 // Byte0 preflight cannot select the label behind a BOM. The explicit UTF8
 // DOCUMENT parser then reaches its required-root check at the original BOM.
 _,e=Parse(ctx,"\xef\xbb\xbf<?xml version='1.0' encoding='MULE_INTERNAL'?><a/>",Options{Mode:Document,InputKind:Binary})
 if !errors.As(e,&malformed)||len(malformed.Diagnostics)!=1||malformed.Diagnostics[0].Condition!="XML_ERR_DOCUMENT_EMPTY"||malformed.Diagnostics[0].Message!="Start tag expected, '<' not found"||malformed.Diagnostics[0].Offset!=0 {t.Fatalf("BOM binary preflight %v",e)}
 for _,tc:=range []struct{s,detail string}{
  {"\xff\xfe<\x00a\x00/\x00>\x00","line 1: internal error: detected an error in element content\n\xff\xfe<\n^"},
  {"<a>\x00</a>","line 1: Premature end of data in tag a line 1\n<a>\n   ^"},
 } {_,e:=Parse(ctx,tc.s,Options{InputKind:Binary});var pe *ParseError;if !errors.As(e,&pe)||pe.Detail()!=tc.detail {t.Fatalf("raw binary %q %v",tc.s,e)}}
}
func TestXMLCoreOutput(t *testing.T) {
 ctx:=context.Background()
 for _,tc:=range []struct{input,want string}{
  {"",""},
  {`<?xml version="1.0" encoding="ISO-8859-1"?><a>é</a>`,`<a>é</a>`},
  {"<?xml version=\"1.0\"?>\n\n<a/>","\n<a/>"},
  {`<?xml version='1.0' standalone='yes'?><a/>`,`<?xml version="1.0" standalone="yes"?><a/>`},
  {`<?xml version='1.1'?><a/>`,`<?xml version="1.1"?><a/>`},
  // Source-derived nondefault version retention (print_xml_decl).
  {`<?xml version='2.0'?><a/>`,`<?xml version="2.0"?><a/>`},
  {`<?xml version='1.0' standalone='no'?>`+"\n<a/>",`<?xml version="1.0" standalone="no"?>`+"\n<a/>"},
  {`<?xml-stylesheet x?><a>&#233;&amp;</a>`,`<?xml-stylesheet x?><a>&#233;&amp;</a>`},
  // Source-derived xml.c315-344/1580-1618; not new oracle observations.
  {"\n<a/>","<a/>"},{"\n\n<a/>","\n<a/>"},
  {`<?xml version=""?><a/>`,`<?xml version=""?><a/>`},
 } {
  r:=accept(t,tc.input,Content);out,w,e:=OutputText(ctx,r)
  if e!=nil||w!=(OutputWarning{})||out!=tc.want||r.Original()!=tc.input {t.Fatalf("typed output got%q warning%+v err%v want%q",out,w,e,tc.want)}
  out,w,e=OutputBinary(ctx,r,6);if e!=nil||w!=(OutputWarning{})||out!=tc.want{t.Fatalf("UTF8 bytes %x warning%+v error%v",out,w,e)}
  out,w,e=OutputBinary(ctx,r,0);if e!=nil||w!=(OutputWarning{})||out!=tc.want{t.Fatal(out,w,e)}
 }
 for _,body:=range []string{"<a/>","<a>é</a>"} {
  r:=accept(t,body,Content);out,w,e:=OutputBinary(ctx,r,8)
  want:=`<?xml version="1.0" encoding="LATIN1"?>`+strings.ReplaceAll(body,"é","\xe9")
  if e!=nil||w!=(OutputWarning{})||out!=want||r.Original()!=body{t.Fatalf("LATIN1 %x warning%+v error%v",out,w,e)}
 }
 r:=accept(t,"<a>漢</a>",Content);out,w,e:=OutputBinary(ctx,r,8);var ee *EncodingError
 if !errors.As(e,&ee)||ee.Kind!=UntranslatableCharacter||ee.SourceName!="UTF8"||ee.DestinationName!="LATIN1"||out!=""||w!=(OutputWarning{}) {t.Fatalf("untranslatable %q warning%+v error%v",out,w,e)}
 out,w,e=OutputBinary(ctx,r,7);if !errors.As(e,&ee)||ee.Kind!=MissingConversion||out!=""||w!=(OutputWarning{}) {t.Fatal(out,w,e)}
 for _,id:=range []int{-1,42} {if out,w,e=OutputBinary(ctx,r,id);e==nil||out!=""||w!=(OutputWarning{}){t.Fatal("target ID bounds")}}
 // Actual pinned capture957 accepts a BOM as CONTENT text, not as DOCUMENT
 // input. No byte0 declaration exists; typed output preserves those characters.
 bom:="\xef\xbb\xbf<a/>"
 bomValue:=accept(t,bom,Content)
 if d:=bomValue.Declaration();d!=(Declaration{}) {t.Fatal(d)}
 out,w,e=OutputText(ctx,bomValue)
 if e!=nil||out!=bom||w!=(OutputWarning{}) {t.Fatalf("BOM output %q warning%+v error%v",out,w,e)}
 out,w,e=OutputBinary(ctx,bomValue,6)
 if e!=nil||out!=bom||w!=(OutputWarning{}) {t.Fatal("UTF8 BOM binary spelling",out,w,e)}
 out,w,e=OutputBinary(ctx,bomValue,8)
 if out!=""||w!=(OutputWarning{})||!errors.As(e,&ee)||ee.Kind!=UntranslatableCharacter {t.Fatal("preserved BOM must fail LATIN1 conversion",out,w,e)}
}
