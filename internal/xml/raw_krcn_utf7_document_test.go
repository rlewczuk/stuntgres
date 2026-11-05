package xml

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
	"unsafe"
)

type rawKRCNUTF7Witness struct { label,body,text string }
func rawKRCNUTF7Witnesses()[]rawKRCNUTF7Witness {
	return []rawKRCNUTF7Witness{
		{"ISO-2022-KR","\x1b$)C\x0eGQ\x0f","한"},
		{"ISO-2022-CN","\x1b$)A\x0eVP\x0f","中"},
		{"UTF-7","+AOk-","é"},
		{"ISO-2022-CN","\x1b$)G\x0eVP\x1bN!!VP\x0f","烘乂烘"},
		{"ISO-2022-CN","\x1b$)G\x0e\x27\x28\x27\x2f\x27\x34\x0f","亠冫勹"},
		{"UTF-7","A+AOk-B+-C","AéB+C"},
		{"UTF-7","+2D3eAA-","😀"},
	}
}

func TestXMLRawKRCNUTF7SourceStyle(t *testing.T) {
	for _,tc:=range rawKRCNUTF7Witnesses(){
		for _,label:=range []string{tc.label,strings.ToLower(tc.label)}{
			for _,style:=range []bool{false,true}{
				prefix,suffix:="<source><v>","</v></source>"
				if style{prefix=`<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="1.0"><xsl:template match="/"><out>`;suffix=`</out></xsl:template></xsl:stylesheet>`}
				for _,bom:=range []string{"","\xef\xbb\xbf"}{
					input:=bom+rawHeader(label)+prefix+tc.body+suffix
					decoded:=rawHeader(label)+prefix+tc.text+suffix
					r:=rawAccept(t,input,decoded,tc.text,RawInputInfo{Codec:tc.label,UTF8BOM:bom!=""},rawDecl(label))
					if unsafe.StringData(r.Original())==unsafe.StringData(input){t.Fatal("converted spelling retained physical transport")}
					_,d,err:=parseExpanded(context.Background(),input,rawOptions)
					root:=rawRoot(d)
					if err!=nil||root==nil{t.Fatal(d,err)}
					if style&&(root.name!="xsl:stylesheet"||len(root.children)!=1||root.children[0].name!="xsl:template"||len(root.children[0].children)!=1||root.children[0].children[0].name!="out"){t.Fatal("not an independently parsed style-shaped tree",root)}
				}
			}
		}
		prefix:=`<!DOCTYPE r [<!ENTITY e "`
		middle:=`"><!ATTLIST r a CDATA "`
		suffix:=`">]><r>&e;</r>`
		input:=rawHeader(tc.label)+prefix+tc.body+middle+tc.body+suffix
		rawAccept(t,input,strings.ReplaceAll(input,tc.body,tc.text),tc.text,RawInputInfo{Codec:tc.label},rawDecl(tc.label))
		_,d,err:=parseExpanded(context.Background(),input,rawOptions);root:=rawRoot(d)
		if err!=nil||root==nil||len(root.attributes)!=1||root.attributes[0].name!="a"||root.attributes[0].value!=tc.text||!root.attributes[0].defaulted{t.Fatal("decoded entity/default ownership",root,err)}
		for _,uri:=range []string{"file:///etc/passwd","http://127.0.0.1:1/no-io","relative.dtd"}{
			for _,external:=range []string{`SYSTEM "`+uri+`"`,`PUBLIC "identifier" "`+uri+`"`}{
				input:=rawHeader(tc.label)+`<!DOCTYPE r [<!ENTITY ext `+external+`>]><r>&ext;`+tc.body+`</r>`
				rawAccept(t,input,strings.ReplaceAll(input,tc.body,tc.text),tc.text,RawInputInfo{Codec:tc.label},rawDecl(tc.label))
			}
			input:=rawHeader(tc.label)+`<!DOCTYPE r SYSTEM "`+uri+`"><r/>`
			rawAccept(t,input,input,"",RawInputInfo{Codec:tc.label},rawDecl(tc.label))
		}
		broken:=rawHeader(tc.label)+`<!DOCTYPE a [<!ENTITY e "<b>`+tc.body+`">]><a>&e;</a>`
		var pe *ParseError
		if err:=rawAtomicFailure(t,broken);!errors.As(err,&pe){t.Fatal("entity failure publication",err)}
		child:=Diagnostic{Condition:"XML_ERR_TAG_NOT_FINISHED",Domain:ParserDomain,Severity:Fatal,Line:1,Column:4+utf8.RuneCountInString(tc.text),Offset:3+len(tc.text),Message:"Premature end of data in tag b line 1",SourceContext:"<b>"+tc.text,Caret:strings.Repeat(" ",3+len(tc.text))+"^",Source:EntitySource,EntityName:"e"}
		if len(pe.Diagnostics)<2||pe.Diagnostics[0]!=child||pe.Diagnostics[len(pe.Diagnostics)-1].Condition!="XML_ERR_UNDECLARED_ENTITY"||pe.Diagnostics[len(pe.Diagnostics)-1].SourceBaseOffset!=len(rawHeader(tc.label))-2{t.Fatal("decoded entity/parent windows",pe.Diagnostics)}
	}
	rawAccept(t,"<r>é</r>","<r>é</r>","é",RawInputInfo{Codec:"UTF8",Default:true},Declaration{})
}

func TestXMLRawKRCNUTF7FeatureSmoke(t *testing.T) {
	for _,tc:=range rawKRCNUTF7Witnesses(){
		for _,style:=range []bool{false,true}{
			prefix,suffix:="<source><v>","</v></source>"
			if style{prefix=`<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:template match="/"><out>`;suffix=`</out></xsl:template></xsl:stylesheet>`}
			rawAccept(t,rawHeader(tc.label)+prefix+tc.body+suffix,rawHeader(tc.label)+prefix+tc.text+suffix,tc.text,RawInputInfo{Codec:tc.label},rawDecl(tc.label))
		}
		bad:="\x80";if tc.label=="UTF-7"{bad="+3AA-"}
		var re *RawInputError
		if err:=rawAtomicFailure(t,rawHeader(tc.label)+"<r/>"+bad);!errors.As(err,&re)||re.Kind!=RawInvalidSequence{t.Fatal("strict closed-root failure",err)}
		rawAccept(t,rawDocument(tc.label,tc.body),rawDocument(tc.label,tc.text),tc.text,RawInputInfo{Codec:tc.label},rawDecl(tc.label))
	}
	for _,tc:=range []struct{label,tail string;buffered int}{
		{"ISO-2022-KR","\x1b$)",3},{"ISO-2022-CN","\x1bN!",3},{"UTF-7","+",1},{"UTF-7","+AA",0},
	}{rawAccept(t,rawHeader(tc.label)+"<r/>"+tc.tail,rawHeader(tc.label)+"<r/>","",RawInputInfo{Codec:tc.label,BufferedBytes:tc.buffered},rawDecl(tc.label))}
}
