package xml

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestXMLRawASCIIJP2Diagnostics(t *testing.T) {
	for _, label := range []string{"ASCII", "US-ASCII"} {
		for _, bom := range []string{"", "\xef\xbb\xbf"} {
			input := bom+rawDocument(label,"é")
			var re *RawInputError
			err := rawAtomicFailure(t,input)
			prefix := rawHeader(label)+"<r>"
			if !errors.As(err,&re) || re.Kind!=RawInvalidSequence || re.Label!=label || re.Codec!="ASCII" || re.RawOffset!=len(bom)+len(prefix) || re.Bytes!="é</" || re.Declaration!=rawDecl(label) || re.Cause==nil || errors.Unwrap(re)!=re.Cause || re.Error()!="invalid raw XML input for ASCII" { t.Fatalf("ASCII provenance %+v",err) }
			want := []Diagnostic{rawExpectedDiagnosticAtBase(prefix,len(rawHeader(label))-2,"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag r line 1",len(prefix),len(prefix)+1)}
			assertRawDiagnostics(t,re.Diagnostics,want,re.Detail())
		}
	}
	label := "ISO-2022-JP-2"
	for _, tc := range []struct { body string; skipped int }{
		{"\x1b$(D!!\x1b(B",4}, {"\x1bNi",0}, {"\x80",0},
		{"\x1b.F\x1bN.",3}, {"\x1b.A\x1b$(D!!\x1b(B",7},
	} {
		for _, root := range []string{"<r>","<r/>"} {
			for _, bom := range []string{"","\xef\xbb\xbf"} {
				input := bom+rawHeader(label)+root+tc.body
				var re *RawInputError
				err := rawAtomicFailure(t,input)
				at := len(bom)+len(rawHeader(label))+len(root)+tc.skipped
				if !errors.As(err,&re) || re.Kind!=RawInvalidSequence || re.Label!=label || re.Codec!=label || re.RawOffset!=at || re.Bytes!=input[at:at+min(4,len(input)-at)] || re.Declaration!=rawDecl(label) || re.Cause==nil || errors.Unwrap(re)!=re.Cause { t.Fatalf("JP2 provenance %x: %+v",input,err) }
				preview := "input conversion failed due to input error, bytes"
				for _, value := range []byte(re.Bytes) { preview+=fmt.Sprintf(" 0x%02X",value) }
				if len(re.Bytes)<4 { preview+=" 0x00" }
				want := []Diagnostic{
					{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:preview},
					{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:preview},
					{Condition:"XML_IO_ENCODER",Domain:IODomain,Severity:Error,Message:"encoder error"},
				}
				if root=="<r>" {
					prefix:=rawHeader(label)+root
					want=append(want,rawExpectedDiagnosticAtBase(prefix,len(rawHeader(label))-2,"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag r line 1",len(prefix),len(prefix)+1))
				}
				assertRawDiagnostics(t,re.Diagnostics,want,re.Detail())
			}
		}
	}
	for _, root := range []string{"<r>","<r/>"} {
		input:=rawHeader(label)+root+"\x1b$X"
		var pe *ParseError
		err:=rawAtomicFailure(t,input)
		if !errors.As(err,&pe) || pe.Category!=GrammarError || pe.Declaration!=rawDecl(label) || pe.SourceBaseOffset!=len(rawHeader(label))-2 { t.Fatal(err) }
		condition,message:="XML_ERR_INVALID_CHAR","PCDATA invalid Char value 27"
		if root=="<r/>" { condition,message="XML_ERR_DOCUMENT_END","Extra content at the end of the document" }
		at:=len(rawHeader(label))+len(root)
		want:=[]Diagnostic{rawExpectedDiagnosticAtBase(input,len(rawHeader(label))-2,Condition(condition),message,at,at+1)}
		// Independently pinned tests-14: literal ESC recovery reaches physical
		// EOF in an unclosed root and reports its separate premature-end event.
		if root=="<r>" { want=append(want,rawExpectedDiagnosticAtBase(input,len(rawHeader(label))-2,"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag r line 1",len(input),len(input)+1)) }
		assertRawDiagnostics(t,pe.Diagnostics,want,pe.Detail())
	}
	for _, label := range []string{"ANSI_X3.4-1968","CSASCII","ISO646-US","ISO_2022_JP_2","ISO_2022-JP-2","ISO-2022-JP2","ISO2022-JP-2","UTF16","UTF-16","UNICODE"} {
		var re *RawInputError
		err:=rawAtomicFailure(t,rawDocument(label,"A"))
		decl:=rawDecl(label);decl.End=0
		if !errors.As(err,&re) || re.Kind!=RawUnavailableCodec || re.Label!=label || re.Codec!="" || re.RawOffset!=len(rawHeader(label))-2 || re.Bytes!="" || re.Cause!=nil || re.Declaration!=decl || len(re.Diagnostics)!=0 || re.Detail()!="" { t.Fatalf("unavailable %s: %+v",label,err) }
	}
	for _, label := range []string{"ASCII","US-ASCII","ISO-2022-JP-2"} {
		// A completed encoding quote selects the converter before close errors.
		input:=rawHeader(label)[:len(rawHeader(label))-2]+"><r/>"
		var pe *ParseError
		err:=rawAtomicFailure(t,input)
		if !errors.As(err,&pe) || pe.Declaration.Encoding!=label || pe.SourceBaseOffset!=len(rawHeader(label))-2 { t.Fatalf("completed quote %s: %+v",label,err) }
		want:=[]Diagnostic{
			rawExpectedDiagnosticAtBase(input,len(rawHeader(label))-2,"XML_ERR_SPACE_REQUIRED","Blank needed here",len(rawHeader(label))-2,len(rawHeader(label))-1),
			rawExpectedDiagnosticAtBase(input,len(rawHeader(label))-2,"XML_ERR_XMLDECL_NOT_FINISHED",`parsing XML declaration: '?>' expected`,len(rawHeader(label))-2,len(rawHeader(label))-1),
		}
		assertRawDiagnostics(t,pe.Diagnostics,want,pe.Detail())
		// Initial BOM does not override an explicitly selected decoder.
		body,text,codec:="A","A","ASCII"
		if label=="ISO-2022-JP-2" { body,text,codec="\x1b$(D+1\x1b(B","é",label }
		rawAccept(t,"\xef\xbb\xbf"+rawDocument(label,body),rawDocument(label,text),text,RawInputInfo{Codec:codec,UTF8BOM:true},rawDecl(label))
		// A later UTF8 BOM is ordinary raw input, never another preamble.
		var later *RawInputError
		if err:=rawAtomicFailure(t,rawDocument(label,"\xef\xbb\xbf"));!errors.As(err,&later) || later.Kind!=RawInvalidSequence || later.RawOffset!=len(rawHeader(label))+3 || later.Bytes!="\xef\xbb\xbf<" { t.Fatalf("later BOM was consumed as transport: %+v",err) }
		rawAtomicFailure(t,"\xef\xbb\xbf\xef\xbb\xbf"+rawDocument(label,body))
	}
	for _, input := range []string{
		`<?xml version="bogus" encoding="ISO-2022-JP-2"?><r>é</r>`,
		`<?xml version="1.0" encoding="1ASCII"?><r>é</r>`,
		`<?xml version="1.0" encoding=ASCII?><r>é</r>`,
	} {
		var pe *ParseError
		err:=rawAtomicFailure(t,input)
		if !errors.As(err,&pe) || pe.Declaration.EncodingPresent { t.Fatalf("malformed declaration selected decoder %+v",err) }
	}
	for _, kind:=range []InputKind{Text,Binary} {
		r,err:=Parse(context.Background(),rawDocument("ASCII","é"),Options{Mode:Document,InputKind:kind})
		if kind==Text && (err!=nil || r.Original()!=rawDocument("ASCII","é") || r.RawInput()!=(RawInputInfo{})) { t.Fatal("Text declaration reinterpreted",r,err) }
		if kind==Binary && (err==nil || !reflect.DeepEqual(r,Result{})) { t.Fatal("Binary gained a new PG encoding",r,err) }
	}
	// Raw error windows never expose a borrowed mutable diagnostic slice.
	warningInput:=strings.Replace(rawDocument(label,"A"),`version="1.0"`,`version="1.1"`,1)
	warning,err:=Parse(context.Background(),warningInput,rawOptions)
	if err!=nil || len(warning.Diagnostics())!=1 { t.Fatal(warning,err) }
	before:=warning.Diagnostics();copy:=warning.Diagnostics();copy[0].Message="caller mutation"
	if !reflect.DeepEqual(warning.Diagnostics(),before) { t.Fatal("result returned borrowed diagnostics") }
	badInput:=strings.Replace(rawDocument(label,"\x1b$(D!!\x1b(B"),`version="1.0"`,`version="1.1"`,1)
	var prior *RawInputError
	if err:=rawAtomicFailure(t,badInput);!errors.As(err,&prior) { t.Fatal(err) }
	wantWarning:=rawExpectedDiagnostic(badInput,"XML_WAR_UNKNOWN_VERSION","Unsupported version '1.1'",19,20)
	wantWarning.Severity=Warning
	prefix:=strings.Replace(rawHeader(label),`version="1.0"`,`version="1.1"`,1)+"<r>"
	preview:="input conversion failed due to input error, bytes 0x21 0x21 0x1B 0x28"
	wantPrior:=[]Diagnostic{
		wantWarning,
		{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:preview},
		{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:preview},
		{Condition:"XML_IO_ENCODER",Domain:IODomain,Severity:Error,Message:"encoder error"},
		rawExpectedDiagnosticAtBase(prefix,len(rawHeader(label))-2,"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag r line 1",len(prefix),len(prefix)+1),
	}
	assertRawDiagnostics(t,prior.Diagnostics,wantPrior,prior.Detail())
	wantDecl:=rawDecl(label);wantDecl.Version="1.1"
	if prior.Declaration!=wantDecl { t.Fatal("prior declaration events lost metadata",prior.Declaration) }
}
