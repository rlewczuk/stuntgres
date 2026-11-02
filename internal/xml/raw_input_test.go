package xml

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

var rawOptions = Options{Mode:Document,InputKind:RawMemory}
func rawHeader(label string) string { return `<?xml version="1.0" encoding="`+label+`"?>` }
func rawDocument(label,body string) string { return rawHeader(label)+"<r>"+body+"</r>" }
func rawRoot(d *document) *node {
	if d!=nil {for _,n:=range d.nodes{if n.kind==elementNode{return n}}}
	return nil
}
func rawText(n *node) string {
	if n==nil{return ""}
	if n.kind==textNode||n.kind==cdataNode{return n.value}
	var b strings.Builder
	for _,child:=range n.children{b.WriteString(rawText(child))}
	return b.String()
}
func rawAccept(t *testing.T,input,decoded,text string,info RawInputInfo,decl Declaration) Result {
	t.Helper()
	var saved Result
	for _,collect:=range []bool{false,true}{
		r,d,err:=parse(context.Background(),input,rawOptions,collect)
		if err!=nil{t.Fatalf("raw %q collect %t: %v",input,collect,err)}
		if !r.Valid()||r.Original()!=decoded||r.RequestedMode()!=Document||r.EffectiveMode()!=Document||r.RawInput()!=info||r.Declaration()!=decl||len(r.Diagnostics())!=0{t.Fatalf("raw %q collect %t: %+v",input,collect,r)}
		if collect {if rawRoot(d)==nil||rawText(rawRoot(d))!=text{t.Fatalf("raw %q tree %+v text %q want %q",input,d,rawText(rawRoot(d)),text)}}else if d!=nil{t.Fatal("validation retained tree")}
		saved=r
	}
	return saved
}
func rawDecl(label string) Declaration {return Declaration{Present:true,Version:"1.0",Encoding:label,EncodingPresent:true,End:len(rawHeader(label))}}
func rawAtomicFailure(t *testing.T,input string) error {
	t.Helper()
	var saved error
	for _,collect:=range []bool{false,true}{
		r,d,err:=parse(context.Background(),input,rawOptions,collect)
		if err==nil||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("failure publication %q: %+v tree %+v error %v",input,r,d,err)}
		if _,ok:=err.(interface{SQLState()string});ok{t.Fatal("package failure acquired SQLSTATE")}
		if saved!=nil&&!reflect.DeepEqual(saved,err){t.Fatalf("validation/collection failures differ: %+v versus %+v",saved,err)}
		saved=err
	}
	if r,err:=Parse(context.Background(),"<healthy/>",rawOptions);err!=nil||!r.Valid(){t.Fatal("failure leaked state",r,err)}
	return saved
}

func TestXMLRawFeatureSmoke(t *testing.T) {
	label:="ISO-2022-JP"
	mixed:="\x1b$B$\"\x1b$B$$\x1b(J\\~\x1b(BZ"
	rawAccept(t,rawDocument(label,mixed),rawDocument(label,"あい¥‾Z"),"あい¥‾Z",RawInputInfo{Codec:label},rawDecl(label))
	rawAccept(t,"<r>é</r>","<r>é</r>","é",RawInputInfo{Codec:"UTF8",Default:true},Declaration{})
	rawAccept(t,rawDocument("LATIN1","\xe9"),rawDocument("LATIN1","é"),"é",RawInputInfo{Codec:"LATIN1"},rawDecl("LATIN1"))
	assertRawEscape(t,rawDocument(label,"\x1b$X"))
	for _,tail:=range []string{"\x1b","\x1b$","\x1b$B$"}{
		buffered:=len(tail);if len(tail)==4{buffered=1}
		rawAccept(t,rawHeader(label)+"<r/>"+tail,rawHeader(label)+"<r/>","",RawInputInfo{Codec:label,BufferedBytes:buffered},rawDecl(label))
	}
	testRawFailureBoundaries(t)
	testRawJOHAB(t)
	testRawISOIllegalTail(t)
	TestXMLRawDTD(t)
}

func TestXMLRawInput(t *testing.T) {
	for _,tc:=range []struct{input,decoded,text string;info RawInputInfo;decl Declaration}{
		{"<r/>","<r/>","",RawInputInfo{Codec:"UTF8",Default:true},Declaration{}},
		{"\xef\xbb\xbf<r>é</r>","<r>é</r>","é",RawInputInfo{Codec:"UTF8",Default:true,UTF8BOM:true},Declaration{}},
		{"\xef\xbb\xbf"+rawDocument("UTF-8","é"),rawDocument("UTF-8","é"),"é",RawInputInfo{Codec:"UTF8",UTF8BOM:true},rawDecl("UTF-8")},
		{"\xef\xbb\xbf"+rawDocument("ISO-8859-1","é"),rawDocument("ISO-8859-1","Ã©"),"Ã©",RawInputInfo{Codec:"LATIN1",UTF8BOM:true},rawDecl("ISO-8859-1")},
		{"\xef\xbb\xbf"+rawDocument("ISO2022JP","\x1b$B$\"\x1b(B"),rawDocument("ISO2022JP","あ"),"あ",RawInputInfo{Codec:"ISO-2022-JP",UTF8BOM:true},rawDecl("ISO2022JP")},
		{"<r>\xef\xbb\xbf</r>","<r>\xef\xbb\xbf</r>","\ufeff",RawInputInfo{Codec:"UTF8",Default:true},Declaration{}},
	}{rawAccept(t,tc.input,tc.decoded,tc.text,tc.info,tc.decl)}
	for _,input:=range []string{"\xef","\xef\xbb","\xef\xbb\xbf","\xef\xbb\xbf\xef\xbb\xbf<r/>"}{
		err:=rawAtomicFailure(t,input);var pe *ParseError
		if !errors.As(err,&pe){t.Fatal(err)}
		source:=input;if strings.HasPrefix(source,"\xef\xbb\xbf"){source=source[3:]}
		want:=[]Diagnostic{{Condition:"XML_ERR_DOCUMENT_EMPTY",Domain:ParserDomain,Severity:Fatal,Line:1,Column:1,Message:"Start tag expected, '<' not found",SourceContext:source,Caret:"^"}}
		assertRawParseError(t,pe,Declaration{},want)
	}
	err:=rawAtomicFailure(t,"");var re *RawInputError
	if !errors.As(err,&re)||re.Kind!=RawEmptyInput||re.Label!=""||re.Codec!=""||re.RawOffset!=0||re.Bytes!=""||re.Declaration!=(Declaration{})||len(re.Diagnostics)!=0||re.Cause!=nil||re.Detail()!=""||re.Error()!="empty raw XML input"{t.Fatalf("empty: %+v",err)}
	for _,opts:=range []Options{{InputKind:RawMemory},{Mode:Document,InputKind:RawMemory+1}}{
		r,err:=Parse(context.Background(),"<r/>",opts);if err==nil||!reflect.DeepEqual(r,Result{}){t.Fatal("invalid options admitted",r,err)}
	}
	input:=rawDocument("ISO-2022-JP","\x1b$B$\"\x1b(B")
	rawAccept(t,input,rawDocument("ISO-2022-JP","あ"),"あ",RawInputInfo{Codec:"ISO-2022-JP"},rawDecl("ISO-2022-JP"))
	if r,err:=Parse(context.Background(),input,Options{Mode:Document,InputKind:Text});err==nil||r.Valid(){t.Fatal("Text reinterpreted raw escape bytes",r,err)}
	for _,kind:=range []InputKind{Text,Binary}{r,err:=Parse(context.Background(),"<r/>",Options{Mode:Document,InputKind:kind});if err!=nil||r.RawInput()!=(RawInputInfo{}){t.Fatal(r,err)}}
}

func TestXMLRawDTD(t *testing.T) {
	label:="ISO-2022-JP";jp:="\x1b$B$\"\x1b(B"
	input:=rawHeader(label)+`<!DOCTYPE r [<!ENTITY e "`+jp+`"><!ATTLIST r a CDATA "`+jp+`">]><r>&e;</r>`
	decoded:=rawHeader(label)+`<!DOCTYPE r [<!ENTITY e "あ"><!ATTLIST r a CDATA "あ">]><r>&e;</r>`
	rawAccept(t,input,decoded,"あ",RawInputInfo{Codec:label},rawDecl(label))
	_,d,err:=parseExpanded(context.Background(),input,rawOptions)
	root:=rawRoot(d)
	if err!=nil||root==nil||len(root.attributes)!=1||root.attributes[0].name!="a"||root.attributes[0].value!="あ"||!root.attributes[0].defaulted{t.Fatalf("decoded defaults: %+v %v",root,err)}
	// Intrinsically empty external entities: URI spellings never select a loader.
	for _,uri:=range []string{"file:///etc/passwd","http://127.0.0.1:1/no-io","relative.dtd"}{
		for _,external:=range []string{`SYSTEM "`+uri+`"`,`PUBLIC "identifier" "`+uri+`"`}{
			s:=rawHeader(label)+`<!DOCTYPE r [<!ENTITY ext `+external+`>]><r>&ext;`+jp+`</r>`
			want:=rawHeader(label)+`<!DOCTYPE r [<!ENTITY ext `+external+`>]><r>&ext;あ</r>`
			rawAccept(t,s,want,"あ",RawInputInfo{Codec:label},rawDecl(label))
		}
		s:=rawHeader(label)+`<!DOCTYPE r SYSTEM "`+uri+`"><r/>`
		rawAccept(t,s,s,"",RawInputInfo{Codec:label},rawDecl(label))
	}
	testRawDTDSourceFrames(t)
}
