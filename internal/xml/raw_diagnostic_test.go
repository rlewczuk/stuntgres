package xml

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// These one-line source windows are derived independently from the documented
// libxml byte-window rule; callback columns/messages below are pinned evidence.
func rawExpectedDiagnostic(source string,code Condition,message string,offset,column int) Diagnostic {
	return rawExpectedDiagnosticAtBase(source,0,code,message,offset,column)
}
func rawExpectedDiagnosticAtBase(source string,base int,code Condition,message string,offset,column int) Diagnostic {
	start:=max(base,offset-80);end:=min(len(source),start+80)
	if nul:=strings.IndexByte(source[start:end],0);nul>=0{end=start+nul}
	return Diagnostic{Condition:code,Domain:ParserDomain,Severity:Fatal,Line:1,Column:column,Offset:offset,SourceBaseOffset:base,Message:message,SourceContext:source[start:end],Caret:strings.Repeat(" ",min(offset-start,79,end-start))+"^"}
}
func assertRawDiagnostics(t *testing.T,got,want []Diagnostic,detail string){
	t.Helper()
	var blocks []string
	for _,d:=range want{if d.Severity>=Error{blocks=append(blocks,"line "+strconv.Itoa(d.Line)+": "+d.Message+"\n"+d.SourceContext+"\n"+d.Caret)}}
	if !reflect.DeepEqual(got,want)||detail!=strings.Join(blocks,"\n"){t.Fatalf("diagnostics\ngot  %+v\nwant %+v\ndetail %q want %q",got,want,detail,strings.Join(blocks,"\n"))}
}
func assertRawParseError(t *testing.T,pe *ParseError,decl Declaration,want []Diagnostic){
	t.Helper()
	base:=0
	if decl.Encoding=="ISO-2022-JP" {base=len(rawHeader(decl.Encoding))-2}
	if pe.Category!=GrammarError||pe.RequestedMode!=Document||pe.EffectiveMode!=Document||pe.SourceBaseOffset!=base||pe.Declaration!=decl||pe.DeclarationDetail!=""||pe.Error()!="invalid XML document"{t.Fatalf("parse error %+v want declaration %+v",pe,decl)}
	assertRawDiagnostics(t,pe.Diagnostics,want,pe.Detail())
}
func assertRawEscape(t *testing.T,input string){
	t.Helper()
	err:=rawAtomicFailure(t,input);var pe *ParseError
	if !errors.As(err,&pe){t.Fatal(err)}
	at:=strings.IndexByte(input,0x1b)
	assertRawParseError(t,pe,rawDecl("ISO-2022-JP"),[]Diagnostic{rawExpectedDiagnosticAtBase(input,len(rawHeader("ISO-2022-JP"))-2,"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 27",at,at+1)})
}

func TestXMLRawDeclarationDiagnostics(t *testing.T){
	badUTF8:="Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x3C 0x2F 0x72"
	type event struct{code Condition;message string;offset,column int}
	for _,tc:=range []struct{input string;decl Declaration;events []event}{
		{`<?xml version="1.0" encoding="1bad"?><r>`+"\xff</r>",Declaration{Present:true,Version:"1.0",End:37},[]event{
			{"XML_ERR_ENCODING_NAME","Invalid XML encoding name",30,31},
			{"XML_ERR_STRING_NOT_CLOSED",`String not closed expecting " or '`,30,31},
			{"XML_ERR_XMLDECL_NOT_FINISHED",`parsing XML declaration: '?>' expected`,30,31},
			{"XML_ERR_INVALID_CHAR",badUTF8,40,35},
		}},
		{`<?xml version="1.0" encoding=BOGUS?><r>`+"\xff</r>",Declaration{Present:true,Version:"1.0",End:36},[]event{
			{"XML_ERR_STRING_NOT_STARTED",`String not started expecting ' or "`,29,30},
			{"XML_ERR_XMLDECL_NOT_FINISHED",`parsing XML declaration: '?>' expected`,29,30},
			{"XML_ERR_INVALID_CHAR",badUTF8,39,34},
		}},
		{`<?xml version="bogus" encoding="BOGUS"?><r>`+"\xff</r>",Declaration{Present:true,End:40},[]event{
			{"XML_ERR_STRING_NOT_CLOSED",`String not closed expecting " or '`,15,16},
			{"XML_ERR_VERSION_MISSING","Malformed declaration expecting version",15,16},
			{"XML_ERR_SPACE_REQUIRED","Blank needed here",15,16},
			{"XML_ERR_XMLDECL_NOT_FINISHED",`parsing XML declaration: '?>' expected`,15,16},
			{"XML_ERR_INVALID_CHAR",badUTF8,43,20},
		}},
		{`<?xml version="1.0" encoding="ISO-2022-JP"><r/>`,Declaration{Present:true,Version:"1.0",Encoding:"ISO-2022-JP",EncodingPresent:true,End:43},[]event{
			{"XML_ERR_SPACE_REQUIRED","Blank needed here",42,43},
			{"XML_ERR_XMLDECL_NOT_FINISHED",`parsing XML declaration: '?>' expected`,42,43},
		}},
		{" "+rawHeader("ISO-2022-JP")+"<r/>",Declaration{},[]event{{"XML_ERR_RESERVED_XML_NAME","XML declaration allowed only at the start of the document",6,7}}},
	}{
		err:=rawAtomicFailure(t,tc.input);var pe *ParseError
		if !errors.As(err,&pe){t.Fatalf("%q: %v",tc.input,err)}
		var want []Diagnostic
		base:=0
		if tc.decl.Encoding=="ISO-2022-JP"{base=42}
		for _,e:=range tc.events{want=append(want,rawExpectedDiagnosticAtBase(tc.input,base,e.code,e.message,e.offset,e.column))}
		assertRawParseError(t,pe,tc.decl,want)
	}
	input:=`<?xml encoding="BOGUS"?><r>`+"\xff</r>"
	err:=rawAtomicFailure(t,input);var re *RawInputError
	if !errors.As(err,&re)||re.Kind!=RawUnknownLabel||re.Label!="BOGUS"||re.Codec!=""||re.RawOffset!=22||re.Bytes!=""||re.Cause!=nil||re.Declaration!=(Declaration{Present:true,Encoding:"BOGUS",EncodingPresent:true}){t.Fatalf("missing version: %+v",err)}
	want:=[]Diagnostic{
		rawExpectedDiagnostic(input,"XML_ERR_VERSION_MISSING","Malformed declaration expecting version",6,7),
		rawExpectedDiagnostic(input,"XML_ERR_SPACE_REQUIRED","Blank needed here",6,7),
		rawExpectedDiagnostic(input,"XML_ERR_UNSUPPORTED_ENCODING","Unsupported encoding BOGUS",22,23),
	}
	assertRawDiagnostics(t,re.Diagnostics,want,re.Detail())
}

func TestXMLRawFailures(t *testing.T){
	for _,tc:=range []struct{label,bytes,preview,codec string}{
		{"ISO-2022-JP","\x80","0x80 0x3C 0x2F 0x72","ISO-2022-JP"},
		{"ISO-8859-6","\xa1","0xA1 0x3C 0x2F 0x72","ISO_8859_6"},
		{"ISO-8859-8","\xa1","0xA1 0x3C 0x2F 0x72","ISO_8859_8"},
	}{
		for _,bom:=range []string{"","\xef\xbb\xbf"}{
			input:=bom+rawDocument(tc.label,tc.bytes);err:=rawAtomicFailure(t,input);var re *RawInputError
			at:=len(rawHeader(tc.label))+3
			if !errors.As(err,&re)||re.Kind!=RawInvalidSequence||re.Label!=tc.label||re.Codec!=tc.codec||re.RawOffset!=len(bom)+at||re.Bytes!=tc.bytes+"</r"||re.Cause==nil||re.Declaration!=rawDecl(tc.label)||re.Error()!="invalid raw XML input for "+tc.codec{t.Fatalf("conversion: %+v",err)}
			if tc.codec=="ISO-2022-JP" {
				if re.Cause.Error()!="invalid byte sequence for ISO-2022-JP at byte 5"{t.Fatal(re.Cause)}
			}else{
				var cause *EncodingError
				id:=26;if tc.codec=="ISO_8859_8"{id=28}
				if !errors.As(re.Cause,&cause)||cause.Kind!=UntranslatableCharacter||cause.Label!=""||cause.SourceID!=id||cause.DestinationID!=6||cause.SourceName!=tc.codec||cause.DestinationName!="UTF8"||cause.Bytes!="0xa1"||cause.Detail!=""||cause.Message!=`character with byte sequence 0xa1 in encoding "`+tc.codec+`" has no equivalent in encoding "UTF8"`{t.Fatalf("mapping hole cause %+v",re.Cause)}
			}
			message:="input conversion failed due to input error, bytes "+tc.preview
			prefix:=rawHeader(tc.label)+"<r>"
			want:=[]Diagnostic{
				{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:message},
				{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:message},
				{Condition:"XML_IO_ENCODER",Domain:IODomain,Severity:Error,Message:"encoder error"},
				rawExpectedDiagnosticAtBase(prefix,len(rawHeader(tc.label))-2,"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag r line 1",at,at+1),
			}
			assertRawDiagnostics(t,re.Diagnostics,want,re.Detail())
		}
	}
	assertRawEscape(t,rawDocument("ISO-2022-JP","\x1b$X"))
	testRawFailureBoundaries(t)
	testRawDirectFailures(t)
	for _,bytes:=range []string{"\x1b","\x1b$","\x1b(I!","\x1b$(D!!","\x1b&@","\x00","\x0eA\x0f","\x1b$B$ ","\x1b$B$\n","\x1b$B$"}{rawAtomicFailure(t,rawDocument("ISO-2022-JP",bytes))}
	for _,tail:=range []string{"\x1b","\x1b$","\x1b$B$"}{rawAtomicFailure(t,rawHeader("ISO-2022-JP")+"<r>"+tail)}
	for _,input:=range []string{"<r/>tail",rawHeader("ISO-2022-JP")+"<r/>\x1b$B$\"\x1b(B","<r>\xff</r>",rawDocument("UTF8","\xff"),rawDocument("EUC_JP","\xa4"),rawDocument("SJIS","\x82\x20"),rawDocument("GB18030","\x94\x39\xfc"),rawDocument("WINDOWS-1252","\x00")}{rawAtomicFailure(t,input)}
}
