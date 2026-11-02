package xml

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func testRawJOHAB(t *testing.T) {
	label:="JOHAB"
	// 8861 is independently observed raw input. 8F41 -> U+B028 follows the
	// JOHAB Hangul bitfields and immutable release map; PG Length calls it a
	// three-byte EUC-style sequence, which is not this raw reader's framing.
	for _,tc:=range []struct{bytes,text string}{
		{"\x88\x61","가"},{"\x8f\x41","\ub028"},
		{"\x88\x61A\x8f\x41\\~","가A\ub028\\~"},
		{"\\~A","\\~A"}, // reuse the existing PG character map's ASCII policy
	}{rawAccept(t,rawDocument(label,tc.bytes),rawDocument(label,tc.text),tc.text,RawInputInfo{Codec:label},rawDecl(label))}
	for _,tc:=range []struct{bytes,causeBytes string;kind EncodingErrorKind}{
		{"\x80A","0x80",InvalidSequence},{"\x83A","0x83",InvalidSequence},
		{"\xd4A","0xd4",InvalidSequence},{"\xd8A","0xd8",InvalidSequence},
		{"\xdfA","0xdf",InvalidSequence},{"\xfaA","0xfa",InvalidSequence},
		{"\x88\x40","0x88 0x40",InvalidSequence},{"\x88\x7f","0x88 0x7f",InvalidSequence},
		{"\x88\x80","0x88 0x80",InvalidSequence},{"\x88\xff","0x88 0xff",InvalidSequence},
		{"\xd9\x30","0xd9 0x30",InvalidSequence},{"\xd9\x7f","0xd9 0x7f",InvalidSequence},
		{"\xd9\x80","0xd9 0x80",InvalidSequence},{"\xd9\xff","0xd9 0xff",InvalidSequence},
		{"\xd9\xe9","0xd9 0xe9",InvalidSequence},{"\xda\xa1","0xda 0xa1",InvalidSequence},
		{"\xde\xf2","0xde 0xf2",InvalidSequence},
		{"\x84\x41","0x84 0x41",UntranslatableCharacter}, // valid frame, unassigned map cell
	}{testRawJOHABFailure(t,rawDocument(label,tc.bytes),rawHeader(label)+"<r>",tc.bytes+"</r>",tc.causeBytes,tc.kind,true)}
	for _,root:=range []string{"<r>","<r/>"}{
		prefix:=rawHeader(label)+root
		testRawJOHABFailure(t,prefix+"\x88",prefix,"\x88","0x88",InvalidSequence,root=="<r>")
	}
}

func testRawJOHABFailure(t *testing.T,input,prefix,fault,causeBytes string,kind EncodingErrorKind,unclosed bool) {
	t.Helper()
	err:=rawAtomicFailure(t,input);var re *RawInputError;var cause *EncodingError
	label:="JOHAB";at:=len(prefix)
	if !errors.As(err,&re)||re.Kind!=RawInvalidSequence||re.Label!=label||re.Codec!=label||re.RawOffset!=at||re.Bytes!=fault[:min(4,len(fault))]||re.Declaration!=rawDecl(label)||!errors.As(re.Cause,&cause){t.Fatalf("JOHAB raw fault %q: %+v",input,err)}
	message:=`invalid byte sequence for encoding "JOHAB": `+causeBytes
	if kind==UntranslatableCharacter{message=`character with byte sequence `+causeBytes+` in encoding "JOHAB" has no equivalent in encoding "UTF8"`}
	if cause.Kind!=kind||cause.SourceID!=40||cause.DestinationID!=6||cause.SourceName!=label||cause.DestinationName!="UTF8"||cause.Label!=""||cause.Detail!=""||cause.Bytes!=causeBytes||cause.Message!=message{t.Fatalf("JOHAB raw cause: %+v",cause)}
	var preview strings.Builder
	preview.WriteString("input conversion failed due to input error, bytes")
	for i:=range min(4,len(fault)){fmt.Fprintf(&preview," 0x%02X",fault[i])}
	if len(fault)<4{preview.WriteString(" 0x00")}
	want:=[]Diagnostic{
		{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:preview.String()},
		{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:preview.String()},
		{Condition:"XML_IO_ENCODER",Domain:IODomain,Severity:Error,Message:"encoder error"},
	}
	if unclosed{want=append(want,rawExpectedDiagnosticAtBase(prefix,len(rawHeader(label))-2,"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag r line 1",at,at+1))}
	assertRawDiagnostics(t,re.Diagnostics,want,re.Detail())
}
