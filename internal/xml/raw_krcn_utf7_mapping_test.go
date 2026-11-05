package xml

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The complete source interpreter and framing corpus predate these decoders.
func TestXMLRawKRCNMappings(t *testing.T) {
	rawKRCNSourceCells(t,func(tc rawSourceCase){
		input:=rawDocument(tc.Label,tc.Body)
		if tc.Class=="scalar" {
			rawAccept(t,input,rawDocument(tc.Label,tc.Decoded),tc.Text,RawInputInfo{Codec:tc.Label},rawDecl(tc.Label));return
		}
		at,bytes,want:=rawKRCNSourceHoleDiagnostics(tc)
		re:=rawKRCNUTF7Fault(t,input,tc.Label,tc.Label,at,bytes)
		assertRawDiagnostics(t,re.Diagnostics,want,re.Detail())
	})
	facts:=rawModernCNS1Facts(t)
	for row:=0x21;row<=0x7e;row++ {for col:=0x21;col<=0x7e;col++ {
		cell:=uint32(row<<8|col)
		if rawCNS1Supplement(uint16(cell))!=facts[cell]{t.Fatalf("supplement source equivalence %04X",cell)}
	}}
}

func rawKRCNUTF7Fault(t *testing.T,input,label,codec string,at int,bytes string)*RawInputError {
	t.Helper()
	var re *RawInputError
	err:=rawAtomicFailure(t,input)
	if !errors.As(err,&re)||re.Kind!=RawInvalidSequence||re.Label!=label||re.Codec!=codec||re.RawOffset!=at||re.Bytes!=bytes||re.Declaration!=rawDecl(label)||re.Cause==nil||errors.Unwrap(re)!=re.Cause||re.Error()!="invalid raw XML input for "+codec{t.Fatalf("raw fault %x: %+v",input,err)}
	bom:=0;if strings.HasPrefix(input,"\xef\xbb\xbf"){bom=3}
	if re.Cause.Error()!=fmt.Sprintf("invalid byte sequence for %s at byte %d",codec,at-bom-(len(rawHeader(label))-2)){t.Fatal("fault cause cursor",re.Cause)}
	return re
}

func rawKRCNUTF7Events(bytes string)[]Diagnostic {
	preview:="input conversion failed due to input error, bytes"
	for _,b:=range []byte(bytes){preview+=fmt.Sprintf(" 0x%02X",b)}
	if len(bytes)<4{preview+=" 0x00"}
	return []Diagnostic{
		{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:preview},
		{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:preview},
		{Condition:"XML_IO_ENCODER",Domain:IODomain,Severity:Error,Message:"encoder error"},
	}
}

func TestXMLRawUTF7(t *testing.T) {
	rawUTF7SourceScalars(func(tc rawSourceCase){
		switch tc.Class {
		case "scalar","empty":
			input:=rawHeader(tc.Label)+"<r><![CDATA["+tc.Body+"]]></r>"
			decoded:=rawHeader(tc.Label)+"<r><![CDATA["+tc.Decoded+"]]></r>"
			rawAccept(t,input,decoded,tc.Text,RawInputInfo{Codec:tc.Label},rawDecl(tc.Label))
		case "conversion","surrogate":
			input:=rawDocument(tc.Label,tc.Body)
			cursor:=0
			if tc.Class=="surrogate"&&strings.HasPrefix(tc.Name,"unit-D") {
				// Name uses uppercase hex. High units withhold output and fail
				// on '-', while low units rewind to the pre-shift checkpoint.
				if tc.Name[6]<'C'{cursor=4}
			}
			at:=len(rawHeader(tc.Label))+3+cursor
			bytes:=input[at:at+min(4,len(input)-at)]
			re:=rawKRCNUTF7Fault(t,input,tc.Label,tc.Label,at,bytes)
			prefix:=rawHeader(tc.Label)+"<r>"
			want:=rawKRCNUTF7Events(bytes)
			want=append(want,rawExpectedDiagnosticAtBase(prefix,len(rawHeader(tc.Label))-2,"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag r line 1",len(prefix),len(prefix)+1))
			assertRawDiagnostics(t,re.Diagnostics,want,re.Detail())
		case "grammar":
			input:=rawHeader(tc.Label)+"<r><![CDATA["+tc.Body+"]]></r>"
			var pe *ParseError
			if err:=rawAtomicFailure(t,input);!errors.As(err,&pe)||pe.Category!=GrammarError||pe.Declaration!=rawDecl(tc.Label)||pe.SourceBaseOffset!=len(rawHeader(tc.Label))-2||len(pe.Diagnostics)==0{t.Fatalf("converted XML-invalid %s: %+v",tc.Name,err)}
		default:t.Fatal("unhandled source class",tc)
		}
	})
	for _,tc:=range rawKRCNUTF7SourceStates(){if tc.Label=="UTF-7"{rawKRCNUTF7StateCase(t,tc,false)}}
	// All residue phases, terminating with explicit '-', punctuation, whitespace
	// or actual markup. Arithmetic constructs units, never a replacing encoder.
	for _,units:=range [][]uint16{{0x00e9},{0x00e9,0x03b3},{0x00e9,0x03b3,0x0041},{0xd800,0xdc00},{0xdbff,0xdfff}} {
		body:=rawUTF7SourceUnits(units...)
		var text string
		for i:=0;i<len(units);i++{u:=units[i];if u>=0xd800&&u<=0xdbff{i++;text+=string(rune(0x10000+(uint32(u)-0xd800)*0x400+uint32(units[i])-0xdc00))}else{text+=string(rune(u))}}
		for _,end:=range []string{"-",",","=","!"," ","\t","\n",""}{
			wire:=body[:len(body)-1]+end
			want:=text+end;if end=="-"{want=text}
			rawAccept(t,rawDocument("UTF-7",wire),rawDocument("UTF-7",want),want,RawInputInfo{Codec:"UTF-7"},rawDecl("UTF-7"))
		}
	}
	// Grammar-invalid conversion must not acquire a raw conversion category.
	for _,body:=range []string{"+AAA-","+ABs-","+//4-","+//8-"}{
		var pe *ParseError
		if err:=rawAtomicFailure(t,rawDocument("UTF-7",body));!errors.As(err,&pe)||pe.Category!=GrammarError{t.Fatal("XML scalar ownership",body,err)}
	}
	if r,d,err:=parseExpanded(context.Background(),rawDocument("UTF-7","+2D3eAA-"),rawOptions);err!=nil||!reflect.DeepEqual(r.RawInput(),RawInputInfo{Codec:"UTF-7"})||rawText(rawRoot(d))!="😀"{t.Fatal(r,d,err)}
}
