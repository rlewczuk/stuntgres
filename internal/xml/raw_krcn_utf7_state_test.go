package xml

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func rawKRCNUTF7StateCase(t *testing.T,tc rawSourceCase,cdata bool) {
	t.Helper()
	input:=rawDocument(tc.Label,tc.Body)
	decoded:=rawDocument(tc.Label,tc.Decoded)
	if cdata{input=rawHeader(tc.Label)+"<r><![CDATA["+tc.Body+"]]></r>";decoded=rawHeader(tc.Label)+"<r><![CDATA["+tc.Decoded+"]]></r>"}
	switch tc.Class {
	case "scalar","empty":rawAccept(t,input,decoded,tc.Text,RawInputInfo{Codec:tc.Label},rawDecl(tc.Label))
	case "conversion","surrogate":
		var re *RawInputError
		if err:=rawAtomicFailure(t,input);!errors.As(err,&re)||re.Kind!=RawInvalidSequence||re.Codec!=tc.Label||re.Cause==nil{t.Fatalf("state %s: %+v",tc.Name,err)}
	case "grammar":
		var pe *ParseError
		if err:=rawAtomicFailure(t,input);!errors.As(err,&pe)||pe.Category!=GrammarError{t.Fatalf("state %s: %+v",tc.Name,err)}
	default:t.Fatal("unhandled state class",tc)
	}
}

func TestXMLRawKRCNState(t *testing.T) {
	for _,tc:=range rawKRCNUTF7SourceStates(){if tc.Label!="UTF-7"{rawKRCNUTF7StateCase(t,tc,false)}}
	rawKRCNSourceStarts(t,func(tc rawSourceCase){rawKRCNUTF7StateCase(t,tc,true)})
	for _,tc:=range rawKRCNSourceEOFTails(){
		input:=rawHeader(tc.Label)+"<r/>"+tc.Tail
		rawAccept(t,input,rawHeader(tc.Label)+"<r/>","",RawInputInfo{Codec:tc.Label,BufferedBytes:tc.Buffered},rawDecl(tc.Label))
		_,s,err:=rawDecode(context.Background(),tc.Tail,0,rawEncoding(tc.Label))
		if err!=nil||s.consumed!=tc.Consumed||s.incomplete!=tc.Buffered||s.fault!=-1||s.size!=0{t.Fatal("physical EOF",tc,s,err)}
		// Real markup supplies bytes absent at EOF: incomplete selected pairs
		// and designation prefixes must not be silently dropped mid-document.
		if tc.Buffered!=0{rawAtomicFailure(t,rawDocument(tc.Label,tc.Tail))}
	}
	for _,label:=range []string{"ISO-2022-KR","ISO-2022-CN"}{
		for _,escape:=range []string{"\x1bX","\x1b$X","\x1b$(","\x1b$)X"}{
			var pe *ParseError
			if err:=rawAtomicFailure(t,rawDocument(label,escape));!errors.As(err,&pe)||pe.Category!=GrammarError{t.Fatal("literal unknown ASCII escape",label,err)}
		}
		for _,control:=range []byte{0,9,10,13,32,127,128,255}{
			rawAtomicFailure(t,rawDocument(label,"\x0e"+string([]byte{control,'!'})+"\x0f"))
		}
	}
}

func TestXMLRawKRCNUTF7Replay(t *testing.T) {
	for _,tc:=range rawUTF7SourceCursors(){
		for _,prefix:=range []string{"","prefix"}{
			decoded,s,err:=rawDecode(context.Background(),prefix+tc.Body,len(prefix),rawUTF7)
			fault:=-1;if tc.Fault{fault=tc.Consumed}
			buffered:=tc.Buffered;if tc.Fault{buffered=0}
			if err!=nil||decoded!=prefix+tc.Prefix||s.size!=len(tc.Prefix)||s.consumed!=tc.Consumed||s.fault!=fault||s.incomplete!=buffered{t.Fatalf("cursor %q %+v decoded %q error %v",tc.Body,s,decoded,err)}
			var out strings.Builder
			replay,e:=utf7Pass(context.Background(),tc.Body,s.consumed,&out)
			if e!=nil||out.String()!=tc.Prefix||replay.consumed!=s.consumed||replay.size!=s.size||replay.fault!=-1||replay.utf7!=s.utf7{t.Fatal("full-input emission checkpoint",tc,replay,s,e)}
			if tc.Fault {
				var retryOut strings.Builder
				retry,e:=utf7PassFrom(context.Background(),tc.Body[s.consumed:],len(tc.Body)-s.consumed,&retryOut,s.utf7)
				if e!=nil||retry.fault!=0||retry.consumed!=0||retry.size!=0||retryOut.Len()!=0||retry.utf7!=s.utf7{t.Fatal("same-handle UTF7 retry",tc,retry,s,e)}
				p:=parser{ctx:context.Background()};if !p.retryRawBuffer(tc.Body[s.consumed:],rawUTF7,s)||p.err!=nil{t.Fatal("reader UTF7 retry",tc,p.err)}
			}
		}
	}
	for _,tc:=range []struct{input string;state utf7State;consumed int}{
		{"+AOncAA-",utf7State{bits:3,count:2,shifted:true},4},
		{"+AOk-+3AA-",utf7State{count:2,shifted:true},4},
		{"+2AAAQQ-",utf7State{count:2,high:0xd800,shifted:true},4},
		{"+AOl-",utf7State{bits:1,count:2,shifted:true},4},
	}{
		_,s,e:=rawDecode(context.Background(),tc.input,0,rawUTF7)
		if e!=nil||s.utf7!=tc.state||s.consumed!=tc.consumed||s.fault!=tc.consumed{t.Fatal("exact UTF7 state",tc,s,e)}
		retry,err:=utf7PassFrom(context.Background(),tc.input[s.consumed:],len(tc.input)-s.consumed,nil,s.utf7)
		if err!=nil||retry.fault!=0||retry.consumed!=0||retry.size!=0||retry.utf7!=tc.state{t.Fatal("exact pending bits/surrogate retry",tc,retry,err)}
	}
	for _,tc:=range []struct{label,body,text string;consumed int;kr bool;cn cnState}{
		{"ISO-2022-KR","\x0eGQ\nGQ\x0f","한",3,true,cnState{}},
		{"ISO-2022-KR","\x0e\x7fA","",1,true,cnState{}},
		{"ISO-2022-CN","\x1b$)G\x0eVP\x1b$)A\x01A","烘",11,false,cnState{active:2,announced:1}},
		{"ISO-2022-CN","\x0eVP\x1bN !","中",3,false,cnState{active:1}},
	}{
		id:=rawEncoding(tc.label)
		decoded,s,e:=rawDecode(context.Background(),tc.body,0,id)
		if e!=nil||decoded!=tc.text||s.consumed!=tc.consumed||s.fault!=tc.consumed||s.kr!=tc.kr||s.cn!=tc.cn{t.Fatal("KR/CN retry state",tc,decoded,s,e)}
		p:=parser{ctx:context.Background()};if !p.retryRawBuffer(tc.body[s.consumed:],id,s)||p.err!=nil{t.Fatal("retained retry state",tc,p.err)}
	}
	// A literal unknown ESC before a later high-byte fault is real output. A
	// clipped input would incorrectly classify ESC as an incomplete EOF tail.
	for _,label:=range []string{"ISO-2022-KR","ISO-2022-CN"}{
		decoded,s,e:=rawDecode(context.Background(),"A\x1bX\x80",0,rawEncoding(label))
		if e!=nil||decoded!="A\x1bX"||s.consumed!=3||s.fault!=3{t.Fatal("physical lookahead",label,decoded,s,e)}
	}
	for _,tail:=range []string{"+","+A","+AA","+2AA","+2D3eA"}{
		buffered:=0;if tail=="+"{buffered=1}
		rawAccept(t,rawHeader("UTF-7")+"<r/>"+tail,rawHeader("UTF-7")+"<r/>","",RawInputInfo{Codec:"UTF-7",BufferedBytes:buffered},rawDecl("UTF-7"))
		if tail!="+"{rawAtomicFailure(t,rawDocument("UTF-7",tail))}
	}
}
