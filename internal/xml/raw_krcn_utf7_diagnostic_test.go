package xml

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestXMLRawKRCNUTF7Diagnostics(t *testing.T) {
	for _,tc:=range []struct{label,body,text string;cursor int}{
		{"ISO-2022-KR","\x1b$)C\x0e~!\x0f","",5},
		{"ISO-2022-KR","\x1b$)C\x0eI!\x0f","",5},
		{"ISO-2022-KR","\x80","",0},
		{"ISO-2022-CN","\x1b$)A\x0e~!\x0f","",5},
		{"ISO-2022-CN","\x1bN !","",0},
		{"ISO-2022-CN","\x7f","",0},
		{"UTF-7","+A-","",2},{"UTF-7","+AA-","",3},
		{"UTF-7","+AOl-","é",4},{"UTF-7","+3AA-","",0},
		{"UTF-7","A+3AA-","A",1},{"UTF-7","+AOncAA-","é",4},
		{"UTF-7","+AOk-+3AA-","é",4},{"UTF-7","+2AA-","",4},
		{"UTF-7","+2AAAQQ-","",4},{"UTF-7","~","",0},{"UTF-7","\\","",0},
	}{
		for _,label:=range []string{tc.label,strings.ToLower(tc.label)}{
			for _,root:=range []string{"<r>","<r/>"}{for _,bom:=range []string{"","\xef\xbb\xbf"}{
				input:=bom+rawHeader(label)+root+tc.body
				at:=len(bom)+len(rawHeader(label))+len(root)+tc.cursor
				bytes:=input[at:at+min(4,len(input)-at)]
				re:=rawKRCNUTF7Fault(t,input,label,tc.label,at,bytes)
				want:=rawKRCNUTF7Events(bytes)
				prefix:=rawHeader(label)+root+tc.text
				if root=="<r>"{want=append(want,rawExpectedDiagnosticAtBase(prefix,len(rawHeader(label))-2,"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag r line 1",len(prefix),len(rawHeader(label))+4+utf8.RuneCountInString(tc.text)))}else if tc.text!=""{
					// Converted trailing text after a complete root is still grammar
					// input, alongside the strict conversion failure.
					want=append(want,rawExpectedDiagnosticAtBase(prefix,len(rawHeader(label))-2,"XML_ERR_DOCUMENT_END","Extra content at the end of the document",len(rawHeader(label))+len(root),len(rawHeader(label))+len(root)+1))
				}
				assertRawDiagnostics(t,re.Diagnostics,want,re.Detail())
			}}
		}
	}
	for _,label:=range []string{"ISO-2022-KR","ISO-2022-CN"}{
		input:=rawDocument(label,"\x1b$X")
		var pe *ParseError
		if err:=rawAtomicFailure(t,input);!errors.As(err,&pe)||pe.Category!=GrammarError||pe.Declaration!=rawDecl(label)||pe.SourceBaseOffset!=len(rawHeader(label))-2{t.Fatal("unknown escape is grammar",err)}
		at:=len(rawHeader(label))+3
		want:=[]Diagnostic{rawExpectedDiagnosticAtBase(input,len(rawHeader(label))-2,"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 27",at,at+1)}
		assertRawDiagnostics(t,pe.Diagnostics,want,pe.Detail())
	}
	for _,label:=range []string{"ISO-2022-CN-EXT","UTF-7-IMAP","UTF-16","ISO2022KR","CSISO2022KR","ISO2022CN","CSISO2022CN","UTF7","UNICODE-1-1-UTF-7"}{
		if rawEncoding(label)!=-1{t.Fatal("unqualified or distinct handler admitted",label)}
		var re *RawInputError
		err:=rawAtomicFailure(t,rawDocument(label,"A"));decl:=rawDecl(label);decl.End=0
		if !errors.As(err,&re)||re.Kind!=RawUnavailableCodec||re.Label!=label||re.Codec!=""||re.RawOffset!=len(rawHeader(label))-2||re.Bytes!=""||re.Cause!=nil||re.Declaration!=decl||len(re.Diagnostics)!=0||re.Detail()!=""{t.Fatalf("honest unavailable %s: %+v",label,err)}
	}
	label:="BOGUS"
	input:=rawDocument(label,"A");var re *RawInputError
	if err:=rawAtomicFailure(t,input);!errors.As(err,&re)||re.Kind!=RawUnknownLabel{t.Fatal("unknown label category",err)}
	decl:=rawDecl(label);decl.End=0
	if re.Declaration!=decl||re.RawOffset!=len(rawHeader(label))-2||re.Bytes!=""||re.Cause!=nil{t.Fatal("unknown provenance",re)}
	assertRawDiagnostics(t,re.Diagnostics,[]Diagnostic{rawExpectedDiagnostic(input,"XML_ERR_UNSUPPORTED_ENCODING","Unsupported encoding BOGUS",len(rawHeader(label))-2,len(rawHeader(label))-1)},re.Detail())
	for _,tc:=range rawKRCNUTF7Witnesses()[:3]{
		label:=tc.label
		input:=rawHeader(label)[:len(rawHeader(label))-2]+"><r/>"
		var pe *ParseError
		if err:=rawAtomicFailure(t,input);!errors.As(err,&pe)||pe.Declaration.Encoding!=label||pe.SourceBaseOffset!=len(rawHeader(label))-2{t.Fatal("quote selection timing",label,err)}
		want:=[]Diagnostic{
			rawExpectedDiagnosticAtBase(input,len(rawHeader(label))-2,"XML_ERR_SPACE_REQUIRED","Blank needed here",len(rawHeader(label))-2,len(rawHeader(label))-1),
			rawExpectedDiagnosticAtBase(input,len(rawHeader(label))-2,"XML_ERR_XMLDECL_NOT_FINISHED",`parsing XML declaration: '?>' expected`,len(rawHeader(label))-2,len(rawHeader(label))-1),
		}
		assertRawDiagnostics(t,pe.Diagnostics,want,pe.Detail())
		for _,kind:=range []InputKind{Text,Binary}{
			r,err:=Parse(context.Background(),rawDocument(label,"é"),Options{Mode:Document,InputKind:kind})
			if kind==Text&&(err!=nil||r.Original()!=rawDocument(label,"é")||r.RawInput()!=(RawInputInfo{})){t.Fatal("Text reinterpreted",r,err)}
			if kind==Binary&&(err==nil||!reflect.DeepEqual(r,Result{})){t.Fatal("Binary acquired raw codec",r,err)}
		}
		for _,body:=range []string{"\xef\xbb\xbf","\x80","\xff"}{
			var fault *RawInputError
			if err:=rawAtomicFailure(t,rawDocument(label,body));!errors.As(err,&fault)||fault.RawOffset!=len(rawHeader(label))+3||fault.Bytes!= (body+"</r>")[:min(4,len(body)+4)]{t.Fatal("physical tail provenance",label,err)}
		}
		for _,bad:=range []string{`<?xml version="bogus" encoding="`+label+`"?><r/>`,`<?xml version="1.0" encoding= `+label+`?><r/>`,`<?xml version="1.0" encoding="1`+label+`"?><r/>`}{
			var pe *ParseError
			if err:=rawAtomicFailure(t,bad);!errors.As(err,&pe)||pe.Declaration.EncodingPresent{t.Fatal("malformed declaration selected decoder",err)}
		}
	}
}
