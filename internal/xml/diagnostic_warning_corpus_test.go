package xml

import (
 "strings"
 "testing"
)

// ASCII-only expectation construction from the independent source-coordinate
// tables. Multiline/multibyte exceptions below specify all fields explicitly.
type parserWarningPoint struct {code Condition;message string;offset int;level Severity}
func parserWarningASCII(input string,points ...parserWarningPoint) []Diagnostic {
 events:=make([]Diagnostic,len(points))
 for i,v:=range points {events[i]=Diagnostic{Condition:v.code,Domain:ParserDomain,Severity:v.level,Line:1,Column:v.offset+1,Offset:v.offset,Message:v.message,SourceContext:input,Caret:strings.Repeat(" ",v.offset)+"^"}}
 return events
}
func TestXMLCoreParserWarnings(t *testing.T) {
 const prefix="xmlParsePITarget: invalid name prefix 'xml'"
 const spaceSuffix="\" for xml:space : \"default\" or \"preserve\" expected"
 wp:=func(at int) parserWarningPoint{return parserWarningPoint{"XML_ERR_RESERVED_XML_NAME",prefix,at,Warning}}
 ws:=func(value string,at int) parserWarningPoint{return parserWarningPoint{"XML_WAR_SPACE_VALUE","Invalid value \""+value+spaceSuffix,at,Warning}}
 wc:=func(value string,at int) parserWarningPoint{return parserWarningPoint{"XML_WAR_CATALOG_PI","Catalog PI syntax error: "+value,at,Warning}}
 fatal:=func(code Condition,message string,at int) parserWarningPoint{return parserWarningPoint{code,message,at,Fatal}}
 z:=func(at int) parserWarningPoint{return fatal("XML_ERR_INVALID_CHAR","xmlParseCharRef: invalid xmlChar value 0",at)}
 a:=func(at int) parserWarningPoint{return fatal("XML_ERR_SPACE_REQUIRED","attributes construct error",at)}
 s:=func(name string,at int) parserWarningPoint{return fatal("XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag "+name+" line 1",at)}
 ex:=func(at int) parserWarningPoint{return fatal("XML_ERR_DOCUMENT_END","Extra content at the end of the document",at)}
 mismatch:=func(at int) parserWarningPoint{return fatal("XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",at)}
 check:=func(name,input string,content,document []parserWarningPoint){t.Helper();checkParserWarningWitness(t,parserWarningWitness{name:name,input:input,content:parserWarningASCII(input,content...),document:parserWarningASCII(input,document...)})}
 same:=func(name,input string,points ...parserWarningPoint){t.Helper();check(name,input,points,points)}
 for _,target:=range []string{"xmlfoo","XMLfoo","Xmlfoo","Xml-stylesheet","XML-model","xml-stylesheetX","xml-model-extra","xmlx","xMl:foo"} {same("target_"+target,"<?"+target+"?><a/>",wp(2+len(target)))}
 for _,target:=range []string{"xml-stylesheet","xml-model","xxml","xm"} {same("target_"+target,"<?"+target+"?><a/>")}
 for _,target:=range []string{"XML","Xml","xML"} {same("target_"+target,"<?"+target+"?><a/>",fatal("XML_ERR_RESERVED_XML_NAME","Invalid PI name",5))}
 input:="<?xml?><a/>"
 checkParserWarningWitness(t,parserWarningWitness{name:"target_exact_lower_start",input:input,contentDeclarationError:true,document:parserWarningASCII(input,fatal("XML_ERR_RESERVED_XML_NAME","XML declaration allowed only at the start of the document",5))})
 same("target_exact_lower_inner","<a><?xml?></a>",fatal("XML_ERR_RESERVED_XML_NAME","XML declaration allowed only at the start of the document",8))
 same("target_exact_upper_inner","<a><?XML?></a>",fatal("XML_ERR_RESERVED_XML_NAME","Invalid PI name",8))
 for _,tc:=range []struct{name,input,source string;offset,column,caret int}{
  {"target_crlf","<a><?xmlfoo\r\n?></a>","<a><?xmlfoo",11,12,11},
  {"target_complex_crlf","<a><?xmlé\r\n?></a>","<a><?xmlé",11,10,10},
 } {
  d:=Diagnostic{Condition:"XML_ERR_RESERVED_XML_NAME",Domain:ParserDomain,Severity:Warning,Line:1,Column:tc.column,Offset:tc.offset,Message:prefix,SourceContext:tc.source,Caret:strings.Repeat(" ",tc.caret)+"^"}
  checkParserWarningWitness(t,parserWarningWitness{name:tc.name,input:tc.input,content:[]Diagnostic{d},document:[]Diagnostic{d}})
 }
 for _,tc:=range []struct{name,value,constructed string;quiet bool}{
  {"bogus","bogus","bogus",false},{"empty","","",false},{"default","default","",true},{"preserve","preserve","",true},
  {"case","Default","Default",false},{"padded"," default "," default ",false},
  {"ref_default","def&#97;ult","",true},{"ref_preserve","pre&#x73;erve","",true},{"ref_leading","&#100;efault","",true},
  {"ref_space","&#32;default"," default",false},{"ref_cr","pre&#xD;serve","pre\rserve",false},
  {"quote","&quot;","\"",false},{"lt","&lt;","<",false},{"apos","&apos;","'",false},
 } {
  input:=`<a xml:space="`+tc.value+`"/>`
  if tc.quiet {same("space_"+tc.name,input)}else{same("space_"+tc.name,input,ws(tc.constructed,15+len(tc.value)))}
 }
 for _,value:=range []string{"&amp;","&#38;"} {input:=`<a xml:space="`+value+`"/>`;check("space_amp_"+value,input,[]parserWarningPoint{ws("&#38;",20)},[]parserWarningPoint{ws("&",20)})}
 input="<a xml:space=\"de\r\nf\ta\rult\"/>"
 d:=Diagnostic{Condition:"XML_WAR_SPACE_VALUE",Domain:ParserDomain,Severity:Warning,Line:2,Column:9,Offset:26,Message:ws("de f a ult",0).message,SourceContext:`ult"/>`,Caret:"    ^"}
 checkParserWarningWitness(t,parserWarningWitness{name:"space_literal_whitespace",input:input,content:[]Diagnostic{d},document:[]Diagnostic{d}})
 input=`<a xml:space="é"/>`;d=parserWarningASCII(input,ws("é",17))[0];d.Column=17
 checkParserWarningWitness(t,parserWarningWitness{name:"space_unicode",input:input,content:[]Diagnostic{d},document:[]Diagnostic{d}})
 same("space_error_omit_valid",`<a xml:space="de&#0;fault"/>`,z(20))
 same("space_error_omit_invalid",`<a xml:space="bog&#0;us"/>`,z(21),ws("bogus",24))
 same("space_error_entity",`<a xml:space="&bad;"/>`,fatal("XML_ERR_UNDECLARED_ENTITY","Entity 'bad' not defined",19),ws("",20))
 points:=[]parserWarningPoint{fatal("XML_ERR_INVALID_CHAR","invalid character in attribute value",17),ws("bog",17),a(17),s("a",17)}
 check("space_error_scalar","<a xml:space=\"bog\x01us\"/>",append(append([]parserWarningPoint(nil),points...),fatal("XML_ERR_INVALID_CHAR","PCDATA invalid Char value 1",17)),append(append([]parserWarningPoint(nil),points...),ex(17)))
 points=[]parserWarningPoint{fatal("XML_ERR_LT_IN_ATTRIBUTE","Unescaped '<' not allowed in attributes values",17),ws("bog",17),a(17),s("a",17)}
 check("space_error_lt",`<a xml:space="bog<us"/>`,append(append([]parserWarningPoint(nil),points...),fatal("XML_ERR_NAME_REQUIRED","error parsing attribute name",20),a(20),s("us",20)),append(append([]parserWarningPoint(nil),points...),ex(17)))
 for _,name:=range []string{"space","x:space","xml:spaceX","XML:space","xmlx:space","p:space"} {same("false_identity_"+name,`<a xmlns:p="http://www.w3.org/XML/1998/namespace" `+name+`="bogus"/>`)}
 same("lang_not_pedantic",`<a xml:lang="!"/>`)
 same("combined_success",`<?xmlfoo?><a xml:space="bogus"/><?XMLfoo?>`,wp(8),ws("bogus",30),wp(40))
 same("combined_mismatch",`<?xmlfoo?><a xml:space="bogus"></b>`,wp(8),ws("bogus",30),mismatch(35))
 same("error_before_warning",`<a x="&#0;" xml:space="bogus"/>`,z(10),ws("bogus",29))
 same("duplicate_space",`<a xml:space="bad" xml:space="worse"/>`,ws("bad",18),ws("worse",36),fatal("XML_ERR_ATTRIBUTE_REDEFINED","Attribute xml:space redefined",36))
 input=`<?xml version="1.1"?><?xmlfoo?><a xml:space="bogus"/>`
 document:=parserWarningASCII(input,parserWarningPoint{"XML_WAR_UNKNOWN_VERSION","Unsupported version '1.1'",19,Warning},wp(29),ws("bogus",51))
 content:=parserWarningASCII(`<?xmlfoo?><a xml:space="bogus"/>`,wp(8),ws("bogus",30))
 for i:=range content {content[i].Offset+=21;content[i].SourceBaseOffset=21}
 checkParserWarningWitness(t,parserWarningWitness{name:"version_combined",input:input,content:content,document:document,declaration:Declaration{Present:true,Version:"1.1",End:21},contentBase:21})
 check("catalog_pi",`<?oasis-xml-catalog broken?><a/>`,nil,[]parserWarningPoint{wc("broken",28)})
 // Complete Attribute2 partial/NULL return controls from supplement864.
 q:=fatal("XML_ERR_ATTRIBUTE_NOT_STARTED","AttValue: \" or ' expected",13)
 points=[]parserWarningPoint{q,ws("(null)",13),a(13),s("a",13)}
 check("space_missing_quote",`<a xml:space=bogus/>`,points,append(append([]parserWarningPoint(nil),points...),ex(13)))
 same("space_missing_value",`<a xml:space=/>`,q,ws("(null)",13))
 same("space_missing_equal",`<a xml:space/>`,fatal("XML_ERR_ATTRIBUTE_WITHOUT_VALUE","Specification mandates value for attribute space",12))
 for _,tc:=range []struct{name,input,value string;offset int}{
  {"unterminated",`<a xml:space="bogus`,"bogus",19},{"open_only",`<a xml:space="`,"",14},
 } {same("space_"+tc.name,tc.input,fatal("XML_ERR_ATTRIBUTE_NOT_FINISHED","AttValue: ' expected",tc.offset),ws(tc.value,tc.offset),a(tc.offset),s("a",tc.offset))}
 same("space_noref_name",`<a xml:space="&"/>`,fatal("XML_ERR_NAME_REQUIRED","xmlParseEntityRef: no name",15),ws("",16))
 input="<a xml:space=\"&\r\n;\"/>"
 content=[]Diagnostic{
  {Condition:"XML_ERR_NAME_REQUIRED",Domain:ParserDomain,Severity:Fatal,Line:1,Column:16,Offset:16,Message:"xmlParseEntityRef: no name",SourceContext:`<a xml:space="&`,Caret:strings.Repeat(" ",15)+"^"},
  {Condition:"XML_WAR_SPACE_VALUE",Domain:ParserDomain,Severity:Warning,Line:2,Column:3,Offset:19,Message:ws(" ;",0).message,SourceContext:`;"/>`,Caret:"  ^"},
 }
 checkParserWarningWitness(t,parserWarningWitness{name:"space_crlf_no_entity",input:input,content:content,document:content})
 points=[]parserWarningPoint{fatal("XML_ERR_INVALID_CHAR","invalid character in attribute value",21),a(21),s("a",21)}
 check("space_default_scalar","<a xml:space=\"default\x01us\"/>",append(append([]parserWarningPoint(nil),points...),fatal("XML_ERR_INVALID_CHAR","PCDATA invalid Char value 1",21)),append(append([]parserWarningPoint(nil),points...),ex(21)))
 same("space_multiple_colon",`<a xml:space:more="bogus"/>`)
 same("space_leading_colon",`<a :xml:space="bogus"/>`)
 // Syntax-only catalog checks: no input here is loaded or resolved.
 for _,tc:=range []struct{name,data string;warning bool;offset int}{
  {"empty_data"," ",true,23},{"keyword","catalog",false,0},{"keyword_suffix","catalogX",false,0},{"missing_equal",`catalog "x"`,false,0},
  {"missing_quote","catalog=x",true,31},{"missing_close",`catalog="x`,true,32},{"trailing",`catalog="" x`,true,34},
  {"valid_empty",`catalog=""`,false,0},{"valid_single","catalog = '' ",false,0},{"literal_amp","broken &amp;",true,34},
 } {
  input:=`<?oasis-xml-catalog `+tc.data+`?><a/>`;var doc []parserWarningPoint
  if tc.warning {value:=tc.data;if tc.name=="empty_data" {value=""};doc=[]parserWarningPoint{wc(value,tc.offset)}}
  check("catalog_"+tc.name,input,nil,doc)
 }
 input="<?oasis-xml-catalog broken\r\nx\rz?><a/>"
 d=Diagnostic{Condition:"XML_WAR_CATALOG_PI",Domain:ParserDomain,Severity:Warning,Line:2,Column:6,Offset:33,Message:"Catalog PI syntax error: broken\nx\nz",SourceContext:"z?><a/>",Caret:"   ^"}
 checkParserWarningWitness(t,parserWarningWitness{name:"catalog_literal_eol",input:input,document:[]Diagnostic{d}})
 for _,tc:=range []struct{name,input string}{
  {"no_data",`<?oasis-xml-catalog?><a/>`},{"inner",`<a><?oasis-xml-catalog broken?></a>`},{"epilog",`<a/><?oasis-xml-catalog broken?>`},{"case",`<?Oasis-xml-catalog broken?><a/>`},
 } {same("catalog_"+tc.name,tc.input)}
 check("catalog_after_pi",`<?go?><?oasis-xml-catalog broken?><a/>`,nil,[]parserWarningPoint{wc("broken",34)})
 input=`<?xml version="1.0"?><?oasis-xml-catalog broken?><a/>`
 checkParserWarningWitness(t,parserWarningWitness{name:"catalog_after_decl",input:input,document:parserWarningASCII(input,wc("broken",49)),declaration:Declaration{Present:true,Version:"1.0",End:21},contentBase:21,output:`<?oasis-xml-catalog broken?><a/>`})
 points=[]parserWarningPoint{fatal("XML_ERR_PI_NOT_FINISHED","ParsePI: PI oasis-xml-catalog never end ...",30)}
 check("catalog_bad_terminated",`<?oasis-xml-catalog broken<a/>`,points,append(append([]parserWarningPoint(nil),points...),fatal("XML_ERR_DOCUMENT_EMPTY","Start tag expected, '<' not found",30)))
 check("catalog_then_error",`<?oasis-xml-catalog broken?><a></b>`,[]parserWarningPoint{mismatch(35)},[]parserWarningPoint{wc("broken",28),mismatch(35)})
 check("catalog_combo",`<?xmlfoo?><?oasis-xml-catalog broken?><a xml:space="bogus"/>`,[]parserWarningPoint{wp(8),ws("bogus",58)},[]parserWarningPoint{wp(8),wc("broken",38),ws("bogus",58)})
 // Genuine binary receive witnesses preserve raw bytes and NUL contexts.
 for _,tc:=range []struct{name,input string;content,document []Diagnostic}{
  {"space_nul","<a xml:space=\"bog\x00us\"/>",nil,nil},
  {"space_ff","<a xml:space=\"bog\xffus\"/>",nil,nil},
  {"catalog_nul","<?oasis-xml-catalog bro\x00ken?><a/>",nil,nil},
  {"catalog_ff","<?oasis-xml-catalog bro\xffken?><a/>",nil,nil},
 } {
  switch tc.name {
  case "space_nul":
   tc.content=parserWarningASCII(`<a xml:space="bog`,fatal("XML_ERR_ATTRIBUTE_NOT_FINISHED","AttValue: ' expected",17),ws("bog",17),a(17),s("a",17));tc.document=tc.content
  case "space_ff":
   tc.content=parserWarningASCII(tc.input,fatal("XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x75 0x73 0x22",17),ws("bog\xffus",21));tc.document=tc.content
  case "catalog_nul":
   tc.content=parserWarningASCII("<?oasis-xml-catalog bro",fatal("XML_ERR_PI_NOT_FINISHED","ParsePI: PI oasis-xml-catalog never end ...",23));tc.document=append(append([]Diagnostic(nil),tc.content...),parserWarningASCII("<?oasis-xml-catalog bro",fatal("XML_ERR_DOCUMENT_EMPTY","Start tag expected, '<' not found",23))...)
  case "catalog_ff":
   tc.content=parserWarningASCII(tc.input,fatal("XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x6B 0x65 0x6E",23));tc.document=append(append([]Diagnostic(nil),tc.content...),parserWarningASCII(tc.input,wc("bro\xffken",29))...)
  }
  checkParserWarningWitness(t,parserWarningWitness{name:tc.name,input:tc.input,content:tc.content,document:tc.document,binaryOnly:true})
 }
 // Independent trailing-EOL supplement confirms message trimming, while the
 // post-close cursor still tracks the original source bytes and lone CR.
 for _,eol:=range []struct{name,value string}{{"lf","\n"},{"crlf","\r\n"},{"cr","\r"}} {
  line,column:=2,3;if eol.value=="\r" {line,column=1,30}
  d:=Diagnostic{Condition:"XML_WAR_CATALOG_PI",Domain:ParserDomain,Severity:Warning,Line:line,Column:column,Offset:28+len(eol.value),Message:"Catalog PI syntax error: broken",SourceContext:"?><a/>",Caret:"  ^"}
  checkParserWarningWitness(t,parserWarningWitness{name:"catalog_trailing_"+eol.name,input:"<?oasis-xml-catalog broken"+eol.value+"?><a/>",document:[]Diagnostic{d}})
 }
}
