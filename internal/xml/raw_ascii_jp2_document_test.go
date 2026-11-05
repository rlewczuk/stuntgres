package xml

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func TestXMLRawASCIIJP2SourceStyle(t *testing.T) {
	var saved []Result
	var spellings []string
	var backing []*byte
	for _, tc := range []struct { label, body, text, codec string }{
		{"ASCII","A","A","ASCII"}, {"US-ASCII","A","A","ASCII"},
		{"ISO-2022-JP-2","A","A","ISO-2022-JP-2"},
		{"ISO-2022-JP-2","\x1b$(D+1\x1b(B","é","ISO-2022-JP-2"},
		{"ISO-2022-JP-2","\x1b$(D\x22\x37\x1b(B","\uff5e","ISO-2022-JP-2"},
		{"ISO-2022-JP-2","\x1b$(CGQ\x1b(B","한","ISO-2022-JP-2"},
		{"ISO-2022-JP-2","\x1b.A\x1bNi\x1b$(CGQ\x1b(B\x1bNq","é한ñ","ISO-2022-JP-2"},
	} {
		for _, style := range []bool{false,true} {
			prefix,suffix := "<source><v>","</v></source>"
			if style { prefix=`<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="1.0"><xsl:template match="/"><out>`;suffix=`</out></xsl:template></xsl:stylesheet>` }
			input:=rawHeader(tc.label)+prefix+tc.body+suffix
			decoded:=rawHeader(tc.label)+prefix+tc.text+suffix
			r:=rawAccept(t,input,decoded,tc.text,RawInputInfo{Codec:tc.codec},rawDecl(tc.label))
			saved=append(saved,r);spellings=append(spellings,decoded);backing=append(backing,unsafe.StringData(r.Original()))
			_,d,err:=parseExpanded(context.Background(),input,rawOptions)
			if err!=nil { t.Fatal(err) }
			root:=rawRoot(d)
			if style && (root.name!="xsl:stylesheet" || len(root.children)!=1 || root.children[0].name!="xsl:template" || len(root.children[0].children)!=1 || root.children[0].children[0].name!="out") { t.Fatalf("not a real stylesheet-shaped tree %+v",root) }
		}
	}
	// Reverse and interleave independent source/style invocations, leaving the
	// first in Roman/G2 state at physical EOF. Neither state crosses documents.
	for _, tail:=range []string{"\x1b(J","\x1b.A","\x1b.F","\x1b$(D"} {
		rawAccept(t,rawHeader("ISO-2022-JP-2")+"<r/>"+tail,rawHeader("ISO-2022-JP-2")+"<r/>","",RawInputInfo{Codec:"ISO-2022-JP-2"},rawDecl("ISO-2022-JP-2"))
		rawAccept(t,rawDocument("US-ASCII","\\~"),rawDocument("US-ASCII","\\~"),"\\~",RawInputInfo{Codec:"ASCII"},rawDecl("US-ASCII"))
		rawAtomicFailure(t,rawDocument("ISO-2022-JP-2","\x1bNi"))
	}
	for _,label:=range []string{"ASCII","US-ASCII"} {
		// Genuine7bit smoke rejects a high physical tail after a complete
		// document without inventing converter or premature-root callbacks.
		var failure *RawInputError
		if err:=rawAtomicFailure(t,rawHeader(label)+"<r/>\x80");!errors.As(err,&failure) || failure.Kind!=RawInvalidSequence || failure.Label!=label || failure.Codec!="ASCII" || failure.RawOffset!=len(rawHeader(label))+4 || failure.Bytes!="\x80" || failure.Cause==nil || len(failure.Diagnostics)!=0 { t.Fatalf("strict ASCII physical tail: %+v",err) }
	}
	for i,r:=range saved { if r.Original()!=spellings[i] || unsafe.StringData(r.Original())!=backing[i] || !r.Valid() || len(r.Diagnostics())!=0 { t.Fatal("source/style retention changed",i,r) } }
	testRawASCIIJP2DTD(t)
}

func testRawASCIIJP2DTD(t *testing.T) {
	t.Helper()
	for _, tc:=range []struct { label,body,text,codec string }{
		{"ASCII","&#233;","é","ASCII"}, {"US-ASCII","A","A","ASCII"},
		{"ISO-2022-JP-2","\x1b$(D+1\x1b(B","é","ISO-2022-JP-2"},
		{"ISO-2022-JP-2","\x1b$(CGQ\x1b(B","한","ISO-2022-JP-2"},
	} {
		prefix:=`<!DOCTYPE r [<!ENTITY e "`
		middle:=`"><!ATTLIST r a CDATA "`
		suffix:=`">]><r>&e;</r>`
		input:=rawHeader(tc.label)+prefix+tc.body+middle+tc.body+suffix
		spelling:=strings.ReplaceAll(input,tc.body,tc.text)
		if tc.label=="ASCII" { spelling=input } // character references are still unexpanded Original
		rawAccept(t,input,spelling,tc.text,RawInputInfo{Codec:tc.codec},rawDecl(tc.label))
		_,d,err:=parseExpanded(context.Background(),input,rawOptions)
		root:=rawRoot(d)
		if err!=nil || root==nil || len(root.attributes)!=1 || root.attributes[0].name!="a" || root.attributes[0].value!=tc.text || !root.attributes[0].defaulted { t.Fatalf("new codec entity/default %+v %v",root,err) }
		for _, uri:=range []string{"file:///etc/passwd","http://127.0.0.1:1/no-io","relative.dtd"} {
			for _, external:=range []string{`SYSTEM "`+uri+`"`,`PUBLIC "identifier" "`+uri+`"`} {
				input:=rawHeader(tc.label)+`<!DOCTYPE r [<!ENTITY ext `+external+`>]><r>&ext;`+tc.body+`</r>`
				decoded:=input
				if tc.label=="ISO-2022-JP-2" { decoded=strings.ReplaceAll(input,tc.body,tc.text) }
				rawAccept(t,input,decoded,tc.text,RawInputInfo{Codec:tc.codec},rawDecl(tc.label))
			}
			input:=rawHeader(tc.label)+`<!DOCTYPE r SYSTEM "`+uri+`"><r/>`
			rawAccept(t,input,input,"",RawInputInfo{Codec:tc.codec},rawDecl(tc.label))
		}
		// Entity-local coordinates and parent source restoration remain distinct
		// after decoding. The failed entity tree must never escape collection.
		broken:=rawHeader(tc.label)+`<!DOCTYPE a [<!ENTITY e "<b>`+tc.body+`">]><a>&e;</a>`
		err=rawAtomicFailure(t,broken)
		var pe *ParseError
		if !errors.As(err,&pe) { t.Fatal(err) }
		child:=Diagnostic{Condition:"XML_ERR_TAG_NOT_FINISHED",Domain:ParserDomain,Severity:Fatal,Line:1,Column:5,Offset:3+len(tc.text),Message:"Premature end of data in tag b line 1",SourceContext:"<b>"+tc.text,Caret:strings.Repeat(" ",3+len(tc.text))+"^",Source:EntitySource,EntityName:"e"}
		if len(pe.Diagnostics)<2 || pe.Diagnostics[0]!=child || pe.Diagnostics[len(pe.Diagnostics)-1].Condition!="XML_ERR_UNDECLARED_ENTITY" || pe.Diagnostics[len(pe.Diagnostics)-1].SourceBaseOffset!=len(rawHeader(tc.label))-2 { t.Fatalf("decoded entity/parent provenance %+v",pe.Diagnostics) }
	}
	if !reflect.DeepEqual((Result{}).RawInput(),RawInputInfo{}) { t.Fatal("zero result provenance changed") }
}
