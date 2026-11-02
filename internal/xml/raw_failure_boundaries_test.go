package xml

import (
	"errors"
	"strings"
	"testing"
)

// Exercised by TestXMLRawFailures. Expectations distinguish physical buffering,
// conversion failure, XML-invalid scalars and the independently observed reader
// pointer/diagnostic discrepancy after a completed root (tests3/5/7).
func testRawFailureBoundaries(t *testing.T) {
	label:="ISO-2022-JP"
	for _,tail:=range []struct{bytes,preview string}{
		{"\x80","0x80 0x00"},{"\x80A","0x80 0x41 0x00"},{"\x80AB","0x80 0x41 0x42 0x00"},
		{"\xff","0xFF 0x00"},{"\xffA","0xFF 0x41 0x00"},
	}{for _,root:=range []string{"<r>","<r/>"}{
		prefix:=rawHeader(label)+root;input:=prefix+tail.bytes
		err:=rawAtomicFailure(t,input);var re *RawInputError
		causeOffset:="5";if root=="<r/>"{causeOffset="6"}
		if !errors.As(err,&re)||re.Kind!=RawInvalidSequence||re.Label!=label||re.Codec!=label||re.RawOffset!=len(prefix)||re.Bytes!=tail.bytes||re.Declaration!=rawDecl(label)||re.Cause==nil||re.Cause.Error()!="invalid byte sequence for ISO-2022-JP at byte "+causeOffset{t.Fatalf("short EOF %q %q: %+v",root,tail.bytes,err)}
		message:="input conversion failed due to input error, bytes "+tail.preview
		want:=[]Diagnostic{
			{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:message},
			{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:message},
			{Condition:"XML_IO_ENCODER",Domain:IODomain,Severity:Error,Message:"encoder error"},
		}
		if root=="<r>"{want=append(want,rawExpectedDiagnosticAtBase(prefix,42,"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag r line 1",len(prefix),len(prefix)+1))}
		assertRawDiagnostics(t,re.Diagnostics,want,re.Detail())
	}}
	for _,tc:=range []struct{body,decoded,bytes,preview,cause string;rawOffset,column int}{
		{"\x1b$B$ \x1b(B","","$ \x1b(","0x24 0x20 0x1B 0x28","8",50,48},
		{"\x1b$B$\n\x1b(B","","$\n\x1b(","0x24 0x0A 0x1B 0x28","8",50,48},
		// JIS $< really maps to ぜ; conversion then fails at the remaining /r>.
		{"\x1b$B$","ぜ","/r>","0x2F 0x72 0x3E 0x00","10",52,49},
	}{
		input:=rawDocument(label,tc.body);err:=rawAtomicFailure(t,input);var re *RawInputError
		if !errors.As(err,&re)||re.Kind!=RawInvalidSequence||re.Label!=label||re.Codec!=label||re.RawOffset!=tc.rawOffset||re.Bytes!=tc.bytes||re.Declaration!=rawDecl(label)||re.Cause==nil||re.Cause.Error()!="invalid byte sequence for ISO-2022-JP at byte "+tc.cause{t.Fatalf("pair boundary %q: %+v",tc.body,err)}
		message:="input conversion failed due to input error, bytes "+tc.preview
		prefix:=rawHeader(label)+"<r>"+tc.decoded
		want:=[]Diagnostic{
			{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:message},
			{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:message},
			{Condition:"XML_IO_ENCODER",Domain:IODomain,Severity:Error,Message:"encoder error"},
			rawExpectedDiagnosticAtBase(prefix,42,"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag r line 1",len(prefix),tc.column),
		}
		assertRawDiagnostics(t,re.Diagnostics,want,re.Detail())
	}
	for _,tail:=range []string{"\x1b","\x1b$","\x1b$B$"}{
		prefix:=rawHeader(label)+"<r>";err:=rawAtomicFailure(t,prefix+tail);var pe *ParseError
		if !errors.As(err,&pe){t.Fatal(err)}
		assertRawParseError(t,pe,rawDecl(label),[]Diagnostic{rawExpectedDiagnosticAtBase(prefix,42,"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag r line 1",47,48)})
	}
	for _,body:=range []string{"\x1b","\x1b$","\x1b(I!","\x1b$(D!!"}{assertRawEscape(t,rawDocument(label,body))}
	for _,tc:=range []struct{body string;offsets []int;messages []string}{
		{"\x00",[]int{47},[]string{"Premature end of data in tag r line 1"}},
		{"\x0eA\x0f",[]int{47,49},[]string{"PCDATA invalid Char value 14","PCDATA invalid Char value 15"}},
	}{
		input:=rawDocument(label,tc.body);err:=rawAtomicFailure(t,input);var pe *ParseError
		if !errors.As(err,&pe){t.Fatal(err)}
		var want []Diagnostic
		for i,at:=range tc.offsets{code:=Condition("XML_ERR_INVALID_CHAR");if tc.body=="\x00"{code="XML_ERR_TAG_NOT_FINISHED"};want=append(want,rawExpectedDiagnosticAtBase(input,42,code,tc.messages[i],at,at+1))}
		assertRawParseError(t,pe,rawDecl(label),want)
	}
	for _,tc:=range []struct{input,decoded string;decl Declaration;base,offset,column int}{
		{"<r/>tail","<r/>tail",Declaration{},0,4,5},
		{rawHeader(label)+"<r/>\x1b$B$\"\x1b(B",rawHeader(label)+"<r/>あ",rawDecl(label),42,48,49},
	}{
		err:=rawAtomicFailure(t,tc.input);var pe *ParseError;if !errors.As(err,&pe){t.Fatal(err)}
		assertRawParseError(t,pe,tc.decl,[]Diagnostic{rawExpectedDiagnosticAtBase(tc.decoded,tc.base,"XML_ERR_DOCUMENT_END","Extra content at the end of the document",tc.offset,tc.column)})
	}
	for _,label:=range []string{"UTF8","UTF-8"}{
		input:=rawDocument(label,"\xff");err:=rawAtomicFailure(t,input);var pe *ParseError;if !errors.As(err,&pe){t.Fatal(err)}
		at:=strings.IndexByte(input,0xff)
		assertRawParseError(t,pe,rawDecl(label),[]Diagnostic{rawExpectedDiagnostic(input,"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x3C 0x2F 0x72",at,at+1)})
	}
}

func testRawDirectFailures(t *testing.T) {
	for _,tc:=range []struct{label,codec,body,bytes,preview,causeBytes string;id int}{
		{"EUC_JP","EUC_JP","\xa4","\xa4</r","0xA4 0x3C 0x2F 0x72","0xa4 0x3c",1},
		{"SJIS","SJIS","\x82 ","\x82 </","0x82 0x20 0x3C 0x2F","0x82 0x20",35},
		{"GB18030","GB18030","\x94\x39\xfc","\x94\x39\xfc<","0x94 0x39 0xFC 0x3C","0x94 0x39 0xfc 0x3c",39},
	}{
		input:=rawDocument(tc.label,tc.body);err:=rawAtomicFailure(t,input);var re *RawInputError;var cause *EncodingError
		prefix:=rawHeader(tc.label)+"<r>";at:=len(prefix);base:=len(rawHeader(tc.label))-2
		if !errors.As(err,&re)||re.Kind!=RawInvalidSequence||re.Label!=tc.label||re.Codec!=tc.codec||re.RawOffset!=at||re.Bytes!=tc.bytes||re.Declaration!=rawDecl(tc.label)||!errors.As(re.Cause,&cause){t.Fatalf("direct fault %s: %+v",tc.label,err)}
		if cause.Kind!=InvalidSequence||cause.Label!=""||cause.SourceID!=tc.id||cause.DestinationID!=6||cause.SourceName!=tc.codec||cause.DestinationName!="UTF8"||cause.Bytes!=tc.causeBytes||cause.Detail!=""||cause.Message!=`invalid byte sequence for encoding "`+tc.codec+`": `+tc.causeBytes{t.Fatalf("direct cause %+v",cause)}
		message:="input conversion failed due to input error, bytes "+tc.preview
		want:=[]Diagnostic{
			{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:message},
			{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:message},
			{Condition:"XML_IO_ENCODER",Domain:IODomain,Severity:Error,Message:"encoder error"},
			rawExpectedDiagnosticAtBase(prefix,base,"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag r line 1",at,at+1),
		}
		assertRawDiagnostics(t,re.Diagnostics,want,re.Detail())
	}
}

func testRawDTDSourceFrames(t *testing.T) {
	label:="ISO-2022-JP"
	input:=rawHeader(label)+`<!DOCTYPE a [<!ENTITY e "<b>">]><a>&e;</a>`
	err:=rawAtomicFailure(t,input);var pe *ParseError
	if !errors.As(err,&pe){t.Fatal(err)}
	// Existing independently qualified DTD frame witness, with a real raw
	// converter switch. Child offsets remain local; parent restores base42.
	want:=[]Diagnostic{
		{Condition:"XML_ERR_TAG_NOT_FINISHED",Domain:ParserDomain,Severity:Fatal,Line:1,Column:4,Offset:3,Message:"Premature end of data in tag b line 1",SourceContext:"<b>",Caret:"   ^",Source:EntitySource,EntityName:"e"},
		rawExpectedDiagnosticAtBase(input,42,"XML_ERR_UNDECLARED_ENTITY","Entity 'e' failed to parse",82,83),
	}
	assertRawParseError(t,pe,rawDecl(label),want)
}
