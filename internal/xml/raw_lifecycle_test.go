package xml

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func TestXMLRawCancellation(t *testing.T){
	for _,cause:=range []error{context.Canceled,context.DeadlineExceeded}{
		for _,tc:=range []struct{phase,input string;after int}{
			{"selectRawEncoding",rawDocument("ISO-2022-JP","A"),1},
			{"rawVerify",rawDocument("LATIN1",strings.Repeat("\xe9",20000)),2},
			{"rawDirectPass",rawDocument("LATIN1",strings.Repeat("\xe9",20000)),2},
			{"rawVerify",rawDocument("JOHAB",strings.Repeat("\x88\x61",20000)),2},
			{"rawDirectPass",rawDocument("JOHAB",strings.Repeat("\x8f\x41",20000)),2},
			{"rawEmit",rawDocument("JOHAB",strings.Repeat("\x8f\x41",20000)),4},
			{"iso2022JPPass",rawDocument("ISO-2022-JP","\x1b$B"+strings.Repeat("$\"",10000)+"\x1b(B"),2},
			{"rawEmit",rawDocument("LATIN1",strings.Repeat("\xe9",20000)),4},
			{"rawEmit",rawDocument("ISO-2022-JP","\x1b$B"+strings.Repeat("$\"",10000)+"\x1b(B"),4},
			{"text",rawDocument("ISO-2022-JP",strings.Repeat("a",20000)),2},
			{"dtd",rawHeader("ISO-2022-JP")+`<!DOCTYPE r [`+strings.Repeat(" ",20000)+`]><r/>`,2},
			{"retryRawBuffer",rawDocument("ISO-2022-JP","\x80"),1},
		}{for _,collect:=range []bool{false,true}{
			c:=&phaseContext{Context:context.Background(),phase:tc.phase,after:tc.after,cause:cause}
			r,d,err:=parse(c,tc.input,rawOptions,collect)
			if !c.admitted||c.seen<tc.after||!errors.Is(err,cause)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("phase %s collect %t admitted %t seen %d result %+v tree %+v error %v",tc.phase,collect,c.admitted,c.seen,r,d,err)}
			if _,ok:=err.(interface{SQLState()string});ok{t.Fatal("context acquired SQLSTATE")}
		}}
		for _,phase:=range []string{"rawEmit","rawVerify","iso2022JPPass"}{
			label,body:="LATIN1","\xe9";if phase=="iso2022JPPass"{label,body="ISO-2022-JP","\x1b$B$\"\x1b(B"}
			c:=&phaseContext{Context:context.Background(),phase:phase,final:true,cause:cause}
			r,d,err:=parseExpanded(c,rawDocument(label,body),rawOptions)
			if !c.admitted||!errors.Is(err,cause)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("after phase %s: %+v %+v %v",phase,r,d,err)}
		}
		// Collector completion followed by final publication must still poll.
		c:=&phaseContext{Context:context.Background(),phase:"finishDocument",final:true,cause:cause}
		r,d,err:=parseExpanded(c,rawDocument("LATIN1","\xe9"),rawOptions)
		if !c.admitted||!errors.Is(err,cause)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatal("publication cancellation",r,d,err)}
	}
	c,cancel:=context.WithCancel(context.Background());cancel()
	if r,err:=Parse(c,"<r/>",rawOptions);!errors.Is(err,context.Canceled)||!reflect.DeepEqual(r,Result{}){t.Fatal(r,err)}
	expired,cancelDeadline:=context.WithDeadline(context.Background(),time.Unix(1,0));defer cancelDeadline()
	if r,err:=Parse(expired,"<r/>",rawOptions);!errors.Is(err,context.DeadlineExceeded)||!reflect.DeepEqual(r,Result{}){t.Fatal(r,err)}
	rawAccept(t,rawDocument("ISO-2022-JP","A"),rawDocument("ISO-2022-JP","A"),"A",RawInputInfo{Codec:"ISO-2022-JP"},rawDecl("ISO-2022-JP"))
}

func TestXMLRawOwnershipConcurrency(t *testing.T){
	input:=rawDocument("ISO-2022-JP","\x1b$B$\"\x1b(J\\~\x1b(B")
	saved:=rawAccept(t,input,rawDocument("ISO-2022-JP","あ¥‾"),"あ¥‾",RawInputInfo{Codec:"ISO-2022-JP"},rawDecl("ISO-2022-JP"))
	original,decl,info:=saved.Original(),saved.Declaration(),saved.RawInput()
	backing:=unsafe.StringData(original)
	warningInput:=strings.Replace(rawDocument("LATIN1","\xe9"),`version="1.0"`,`version="1.1"`,1)
	warning,err:=Parse(context.Background(),warningInput,rawOptions);if err!=nil||len(warning.Diagnostics())!=1{t.Fatal(warning,err)}
	diagnostics:=warning.Diagnostics();before:=warning.Diagnostics();diagnostics[0].Message="caller mutation"
	if !reflect.DeepEqual(warning.Diagnostics(),before){t.Fatal("borrowed result diagnostics")}
	failure:=rawAtomicFailure(t,rawDocument("ISO-2022-JP","\x80"))
	var re *RawInputError;if !errors.As(failure,&re){t.Fatal(failure)}
	failureEvents:=append([]Diagnostic(nil),re.Diagnostics...);failureBytes:=re.Bytes
	if unsafe.StringData(decl.Version)!=unsafe.StringData(original[15:])||unsafe.StringData(decl.Encoding)!=unsafe.StringData(original[30:]){t.Fatal("converted metadata retained superseded transport backing")}
	const workers=8
	start:=make(chan struct{});failures:=make(chan error,workers)
	var wg sync.WaitGroup
	for worker:=range workers{wg.Add(1);go func(worker int){defer wg.Done();<-start
		for iteration:=range 20{
			tc:=rawCodecWitnesses[(worker*len(rawCodecWitnesses)/workers+iteration)%len(rawCodecWitnesses)]
			label:=strings.Fields(tc.labels)[0]
			r,d,e:=parseExpanded(context.Background(),rawDocument(label,tc.bytes),rawOptions)
			if e!=nil||!r.Valid()||r.Original()!=rawDocument(label,tc.text)||rawText(rawRoot(d))!=tc.text||r.RawInput().Codec!=tc.codec{failures<-errors.New("mixed direct-codec state leakage");return}
			// Leave the prior invocation in Roman at physical EOF. The next
			// invocation still begins in ASCII, including syntax and body.
			r,d,e=parseExpanded(context.Background(),rawHeader("ISO-2022-JP")+"<r/>\x1b(J",rawOptions)
			if e!=nil||!r.Valid(){failures<-errors.New("terminal designation rejected");return}
			r,d,e=parseExpanded(context.Background(),rawDocument("ISO-2022-JP","\\~"),rawOptions)
			if e!=nil||rawText(rawRoot(d))!="\\~"{failures<-errors.New("Roman designation survived invocation");return}
			r,d,e=parseExpanded(context.Background(),rawDocument("ISO-2022-JP","\x80"),rawOptions)
			if e==nil||!reflect.DeepEqual(r,Result{})||d!=nil{failures<-errors.New("partial failure publication");return}
			r,d,e=parseExpanded(context.Background(),input,rawOptions)
			if e!=nil||r.Original()!=original||rawText(rawRoot(d))!="あ¥‾"{failures<-errors.New("failure poisoned healthy invocation");return}
		}
	}(worker)}
	close(start);wg.Wait();close(failures);for err:=range failures{t.Error(err)}
	if saved.Original()!=original||unsafe.StringData(saved.Original())!=backing||saved.Declaration()!=decl||saved.RawInput()!=info||!reflect.DeepEqual(warning.Diagnostics(),before)||!reflect.DeepEqual(re.Diagnostics,failureEvents)||re.Bytes!=failureBytes{t.Fatal("later operations mutated retained values")}
}

func TestXMLRawLimits(t *testing.T){
	for _,tc:=range []struct{current,additional int;ok bool}{{0,0,true},{0x3ffffffe,0,true},{0x3ffffffe,1,false},{0x3fffffff,0,false},{math.MaxInt,1,false},{1,math.MaxInt,false},{-1,1,false},{1,-1,false}}{
		if appendSize(tc.current,tc.additional)!=tc.ok{t.Fatal(tc)}
	}
	limit:=int((^uint(0)>>1)/4)
	if !conversionInputSize(limit-1)||conversionInputSize(limit)||conversionInputSize(math.MaxInt)||conversionInputSize(-1){t.Fatal("conversion guard boundary")}
	for _,id:=range []int{8,rawISO2022JP}{
		decoded,status,err:=rawDecode(context.Background(),"prefix\x1b$B$\"\x1b(B",6,id)
		if err!=nil||status.fault!=-1||status.incomplete!=0||!strings.HasPrefix(decoded,"prefix"){t.Fatal(decoded,status,err)}
	}
	for _,tc:=range []struct{input string;incomplete,consumed,size int}{
		{"\x1b",1,0,0},{"\x1b$",2,0,0},{"\x1b$B$",1,3,0},{"\x1b$B$\"",0,5,3},
	}{status,err:=iso2022JPPass(context.Background(),tc.input,len(tc.input),nil);if err!=nil||status.fault!=-1||status.incomplete!=tc.incomplete||status.consumed!=tc.consumed||status.size!=tc.size{t.Fatal(tc,status,err)}}
	for _,tc:=range []struct{input string;width int}{
		{"A",1},{"\x88\x61",2},{"\x8f\x41",2},{"\x84\x41",2},{"\xd9\xe8",2},{"\xda\xa0",2},{"\xda\xd4",2},{"\xde\xf1",2},
		{"\x88",-1},{"\x83A",-1},{"\xd8A",-1},{"\xdfA",-1},{"\xfaA",-1},{"\x88\x40",-1},{"\x88\x7f",-1},{"\x88\x80",-1},{"\x88\xff",-1},
		{"\xd9\x30",-1},{"\xd9\x7f",-1},{"\xd9\x80",-1},{"\xd9\xe9",-1},{"\xda\xa1",-1},{"\xde\xf2",-1},
	}{if got:=rawJOHABWidth(tc.input);got!=tc.width{t.Fatalf("JOHAB frame %x: %d want %d",tc.input,got,tc.width)}}
	for _,pair:=range [][2]byte{{0x20,0x21},{0x21,0x20},{0x7f,0x21},{0x21,0x7f}}{if jis0208Rune(pair[0],pair[1])!=0{t.Fatal("out-of-range JIS cell")}}
}
