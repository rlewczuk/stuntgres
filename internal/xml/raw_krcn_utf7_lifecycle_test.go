package xml

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

func TestXMLRawKRCNUTF7Lifecycle(t *testing.T) {
	type retained struct{result Result;original string;backing *byte;decl Declaration;info RawInputInfo;failure *RawInputError;events []Diagnostic;bytes string;bytesBacking *byte;cause error}
	var saved []retained
	for _,tc:=range rawKRCNUTF7Witnesses(){
		input:=rawDocument(tc.label,tc.body)
		r:=rawAccept(t,input,rawDocument(tc.label,tc.text),tc.text,RawInputInfo{Codec:tc.label},rawDecl(tc.label))
		original,decl:=r.Original(),r.Declaration()
		if unsafe.StringData(decl.Version)!=unsafe.StringData(original[15:])||unsafe.StringData(decl.Encoding)!=unsafe.StringData(original[30:]){t.Fatal("metadata retained superseded transport")}
		bad:="\x80";if tc.label=="UTF-7"{bad="+AOk-+3AA-"}
		failureInput:=rawDocument(tc.label,bad)
		var failure,other *RawInputError
		if err:=rawAtomicFailure(t,failureInput);!errors.As(err,&failure){t.Fatal(err)}
		if err:=rawAtomicFailure(t,failureInput);!errors.As(err,&other){t.Fatal(err)}
		if failure==other||&failure.Diagnostics[0]==&other.Diagnostics[0]||unsafe.StringData(failure.Bytes)==unsafe.StringData(failureInput[failure.RawOffset:]){t.Fatal("borrowed error ownership")}
		before:=append([]Diagnostic(nil),failure.Diagnostics...)
		other.Diagnostics[0].Message="caller mutation"
		if !reflect.DeepEqual(failure.Diagnostics,before){t.Fatal("errors share diagnostic storage")}
		saved=append(saved,retained{r,original,unsafe.StringData(original),decl,r.RawInput(),failure,before,failure.Bytes,unsafe.StringData(failure.Bytes),failure.Cause})
		warning,err:=Parse(context.Background(),strings.Replace(input,`version="1.0"`,`version="1.1"`,1),rawOptions)
		if err!=nil||len(warning.Diagnostics())!=1{t.Fatal(warning,err)}
		warnings:=warning.Diagnostics();copy:=warning.Diagnostics();copy[0].Message="caller mutation"
		if !reflect.DeepEqual(warning.Diagnostics(),warnings){t.Fatal("borrowed result diagnostic storage")}
	}
	const workers=8
	start:=make(chan struct{});failures:=make(chan error,workers)
	var wg sync.WaitGroup
	for worker:=range workers{wg.Add(1);go func(worker int){defer wg.Done();<-start
		witnesses:=rawKRCNUTF7Witnesses()
		for iteration:=range 30{
			tc:=witnesses[(worker+iteration)%len(witnesses)]
			for _,style:=range []bool{false,true}{
				prefix,suffix:="<source><v>","</v></source>"
				if style{prefix="<style><template><out>";suffix="</out></template></style>"}
				input:=rawHeader(tc.label)+prefix+tc.body+suffix
				r,d,e:=parseExpanded(context.Background(),input,rawOptions)
				if e!=nil||r.Original()!=rawHeader(tc.label)+prefix+tc.text+suffix||rawText(rawRoot(d))!=tc.text||r.RawInput().Codec!=tc.label{failures<-errors.New("source/style invocation state leakage");return}
			}
			// Each family leaves distinct unfinished state on a completed root;
			// no active selection, announcement, pending bits or high escapes.
			for _,tail:=range []struct{label,body string;buffered int}{
				{"ISO-2022-KR","\x0e",0},{"ISO-2022-CN","\x1b$)G\x0e",0},
				{"UTF-7","+2AA",0},{"UTF-7","+",1},
			}{
				r,_,e:=parseExpanded(context.Background(),rawHeader(tail.label)+"<r/>"+tail.body,rawOptions)
				if e!=nil||r.RawInput().BufferedBytes!=tail.buffered{failures<-errors.New("terminal state rejected");return}
				r,d,e:=parseExpanded(context.Background(),rawDocument(tail.label,"A"),rawOptions)
				if e!=nil||rawText(rawRoot(d))!="A"||!r.Valid(){failures<-errors.New("terminal state crossed documents");return}
			}
			bad:="\x80";if tc.label=="UTF-7"{bad="+3AA-"}
			r,d,e:=parseExpanded(context.Background(),rawHeader(tc.label)+"<r/>"+bad,rawOptions)
			if e==nil||!reflect.DeepEqual(r,Result{})||d!=nil{failures<-errors.New("strict failure published partial state");return}
		}
	}(worker)}
	close(start);wg.Wait();close(failures);for err:=range failures{t.Error(err)}
	for _,s:=range saved{
		if s.result.Original()!=s.original||unsafe.StringData(s.result.Original())!=s.backing||s.result.Declaration()!=s.decl||s.result.RawInput()!=s.info||s.failure.Bytes!=s.bytes||unsafe.StringData(s.failure.Bytes)!=s.bytesBacking||s.failure.Cause!=s.cause||s.failure.Declaration!=s.decl||!reflect.DeepEqual(s.failure.Diagnostics,s.events){t.Fatal("retained backing/metadata/error mutated")}
	}
}

func TestXMLRawKRCNUTF7Cancellation(t *testing.T) {
	for _,cause:=range []error{context.Canceled,context.DeadlineExceeded}{
		for _,tc:=range rawKRCNUTF7Witnesses()[:3]{
			long:=strings.Repeat(tc.body,10000)
			zero:="\x1b$)C";pass:="iso2022KRPass"
			if tc.label=="ISO-2022-CN"{zero="\x1b$)G\x1b$*H";pass="iso2022CNPass"}
			if tc.label=="UTF-7"{zero="+!";pass="utf7PassFrom"}
			bad:="\x80";if tc.label=="UTF-7"{bad="+AOk-+3AA-"}
			for _,phase:=range []struct{name,body string;after int;final bool}{
				{"selectRawEncoding",tc.body,1,false},{"rawDecode",long,4,false},
				{pass,long,3,false},{pass,strings.Repeat(zero,10000),3,false},
				{"rawEmit",long,4,false},{"rawEmit",tc.body,0,true},
				{"retryRawBuffer",bad,1,false},{"top",tc.body,0,true},
			}{for _,collect:=range []bool{false,true}{
				c:=&phaseContext{Context:context.Background(),phase:phase.name,after:phase.after,final:phase.final,cause:cause}
				r,d,err:=parse(c,rawDocument(tc.label,phase.body),rawOptions,collect)
				if !c.admitted||!errors.Is(err,cause)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("%s/%s collect %t admitted %t seen %d result %+v tree %+v error %v",phase.name,tc.label,collect,c.admitted,c.seen,r,d,err)}
				if _,ok:=err.(interface{SQLState()string});ok{t.Fatal("context acquired SQLSTATE")}
				rawAccept(t,rawDocument(tc.label,tc.body),rawDocument(tc.label,tc.text),tc.text,RawInputInfo{Codec:tc.label},rawDecl(tc.label))
			}}
			c:=&phaseContext{Context:context.Background(),phase:"finishDocument",final:true,cause:cause}
			r,d,err:=parseExpanded(c,rawDocument(tc.label,tc.body),rawOptions)
			if !c.admitted||!errors.Is(err,cause)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatal("collector prepublication cancellation",r,d,err)}
		}
	}
}

func TestXMLRawKRCNUTF7Limits(t *testing.T) {
	for _,tc:=range []struct{current,additional int;ok bool}{
		{0,0,true},{0x3ffffffe,0,true},{0x3ffffffe,1,false},{0x3fffffff,0,false},
		{math.MaxInt,1,false},{1,math.MaxInt,false},{-1,1,false},{1,-1,false},
	}{if appendSize(tc.current,tc.additional)!=tc.ok{t.Fatal("checked growth",tc)}}
	limit:=int((^uint(0)>>1)/4)
	if !conversionInputSize(limit-1)||conversionInputSize(limit)||conversionInputSize(math.MaxInt)||conversionInputSize(-1){t.Fatal("checked conversion input")}
	for _,id:=range []int{rawISO2022KR,rawISO2022CN,rawUTF7}{
		for _,size:=range []int{-1,0x3fffffff,math.MaxInt}{
			decoded,err:=rawEmit(context.Background(),"",0,id,rawDecodeStatus{size:size,fault:-1})
			if err==nil||decoded!=""{t.Fatal("emission allocated/published an unchecked size",id,size,decoded,err)}
		}
	}
	for _,tc:=range rawKRCNUTF7Witnesses(){
		body,text:=strings.Repeat(tc.body,10000),strings.Repeat(tc.text,10000)
		rawAccept(t,rawDocument(tc.label,body),rawDocument(tc.label,text),text,RawInputInfo{Codec:tc.label},rawDecl(tc.label))
	}
	for _,tc:=range []rawKRCNUTF7Witness{
		{"ISO-2022-KR",strings.Repeat("\x1b$)C\x0e\x0f",10000),""},
		{"ISO-2022-CN",strings.Repeat("\x1b$)G\x1b$*H\x0e\x0f",10000),""},
		{"UTF-7",strings.Repeat("+-",10000),strings.Repeat("+",10000)},
	}{rawAccept(t,rawDocument(tc.label,tc.body),rawDocument(tc.label,tc.text),tc.text,RawInputInfo{Codec:tc.label},rawDecl(tc.label))}
}
