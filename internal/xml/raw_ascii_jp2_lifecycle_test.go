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

func TestXMLRawASCIIJP2Lifecycle(t *testing.T) {
	label:="ISO-2022-JP-2"
	input:=rawDocument(label,"\x1b.A\x1bNi\x1b$(CGQ\x1b(B\x1bNq")
	saved:=rawAccept(t,input,rawDocument(label,"é한ñ"),"é한ñ",RawInputInfo{Codec:label},rawDecl(label))
	original,decl,info:=saved.Original(),saved.Declaration(),saved.RawInput()
	backing:=unsafe.StringData(original)
	if unsafe.StringData(decl.Version)!=unsafe.StringData(original[15:]) || unsafe.StringData(decl.Encoding)!=unsafe.StringData(original[30:]) { t.Fatal("converted metadata borrowed old transport backing") }
	var failure *RawInputError
	if err:=rawAtomicFailure(t,rawDocument(label,"\x1b.A\x1b$(D!!\x1b(B")); !errors.As(err,&failure) { t.Fatal(err) }
	events:=append([]Diagnostic(nil),failure.Diagnostics...)
	bytes,cause,declaration:=failure.Bytes,failure.Cause.Error(),failure.Declaration
	warning,err:=Parse(context.Background(),strings.Replace(input,`version="1.0"`,`version="1.1"`,1),rawOptions)
	if err!=nil || len(warning.Diagnostics())!=1 { t.Fatal(warning,err) }
	warnings:=warning.Diagnostics();before:=warning.Diagnostics();warnings[0].Message="caller mutation"
	if !reflect.DeepEqual(warning.Diagnostics(),before) { t.Fatal("borrowed diagnostic slice") }
	const workers=8
	start:=make(chan struct{})
	failures:=make(chan error,workers)
	var wg sync.WaitGroup
	for worker:=range workers {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done();<-start
			for iteration:=range 20 {
				labels:=[]string{"ASCII","US-ASCII","ISO-2022-JP-2","LATIN1","ISO-2022-JP"}
				current:=labels[(worker+iteration)%len(labels)]
				body,text,codec:="A","A","ASCII"
				switch current {
				case "ISO-2022-JP-2": body,text,codec="\x1b.A\x1bNi\x1b$(CGQ\x1b(B\x1bNq","é한ñ",current
				case "LATIN1": body,text,codec="\xe9","é",current
				case "ISO-2022-JP": body,text,codec="\x1b$B$\"\x1b(B","あ",current
				}
				r,d,e:=parseExpanded(context.Background(),rawDocument(current,body),rawOptions)
				if e!=nil || r.Original()!=rawDocument(current,text) || rawText(rawRoot(d))!=text || r.RawInput().Codec!=codec { failures<-errors.New("mixed invocation state leakage");return }
				for _,tail:=range []string{"\x1b(J","\x1b.A","\x1b.F"} {
					r,_,e=parseExpanded(context.Background(),rawHeader(label)+"<r/>"+tail,rawOptions)
					if e!=nil || !r.Valid() { failures<-errors.New("valid final designation rejected");return }
					r,d,e=parseExpanded(context.Background(),rawDocument(label,"\\~"),rawOptions)
					if e!=nil || rawText(rawRoot(d))!="\\~" { failures<-errors.New("G0 leaked between documents");return }
					r,d,e=parseExpanded(context.Background(),rawDocument(label,"\x1bNi"),rawOptions)
					if e==nil || !reflect.DeepEqual(r,Result{}) || d!=nil { failures<-errors.New("G2 leaked or partial failure published");return }
				}
				r,d,e=parseExpanded(context.Background(),input,rawOptions)
				if e!=nil || r.Original()!=original || rawText(rawRoot(d))!="é한ñ" { failures<-errors.New("failure poisoned healthy reuse");return }
			}
		}(worker)
	}
	close(start);wg.Wait();close(failures)
	for err:=range failures { t.Error(err) }
	if saved.Original()!=original || unsafe.StringData(saved.Original())!=backing || saved.Declaration()!=decl || saved.RawInput()!=info || !reflect.DeepEqual(warning.Diagnostics(),before) || failure.Bytes!=bytes || failure.Cause.Error()!=cause || failure.Declaration!=declaration || !reflect.DeepEqual(failure.Diagnostics,events) { t.Fatal("later calls mutated retained results/errors") }
}

func TestXMLRawASCIIJP2Cancellation(t *testing.T) {
	for _,cause:=range []error{context.Canceled,context.DeadlineExceeded} {
		for _,tc:=range []struct { phase,label,body string; after int; final bool }{
			{"rawDecode","ASCII",strings.Repeat("a",20000),3,false},
			{"rawDecode","US-ASCII",strings.Repeat("a",20000),4,false},
			{"rawDecode","ISO-2022-JP-2",strings.Repeat("\x1b.A\x1b.F\x1b(B",10000),4,false},
			{"rawDecode","ISO-2022-JP-2","\x1b.A"+strings.Repeat("\x1bNi",10000),4,false},
			{"rawEmit","ISO-2022-JP-2",strings.Repeat("\x1b$(CGQ\x1b(B",10000),4,false},
			{"rawEmit","ISO-2022-JP-2","\x1b$(D+1\x1b(B",0,true},
			{"retryRawBuffer","ISO-2022-JP-2","\x1b.A\x1b$(D!!\x1b(B",1,false},
			{"top","ASCII",strings.Repeat("A",20000),0,true},
			{"top","ISO-2022-JP-2","\x1b$(D"+strings.Repeat("+1",10000)+"\x1b(B",0,true},
		} {
			for _,collect:=range []bool{false,true} {
				c:=&phaseContext{Context:context.Background(),phase:tc.phase,after:tc.after,final:tc.final,cause:cause}
				r,d,err:=parse(c,rawDocument(tc.label,tc.body),rawOptions,collect)
				if !c.admitted || !errors.Is(err,cause) || !reflect.DeepEqual(r,Result{}) || d!=nil { t.Fatalf("%s/%s collect %t admitted %t seen %d result %+v tree %+v error %v",tc.phase,tc.label,collect,c.admitted,c.seen,r,d,err) }
				if _,ok:=err.(interface{SQLState()string});ok { t.Fatal("ordinary cancellation acquired SQLSTATE") }
				if r,err:=Parse(context.Background(),rawDocument("ASCII","A"),rawOptions);err!=nil || !r.Valid() { t.Fatal("cancellation leaked",r,err) }
			}
		}
		// finishDocument belongs only to collection; validation's publication
		// poll is covered by the completed top production above.
		for _,tc:=range []struct { label,body string }{{"ASCII","A"},{"ISO-2022-JP-2","\x1b$(D+1\x1b(B"}} {
			c:=&phaseContext{Context:context.Background(),phase:"finishDocument",final:true,cause:cause}
			r,d,err:=parseExpanded(c,rawDocument(tc.label,tc.body),rawOptions)
			if !c.admitted || !errors.Is(err,cause) || !reflect.DeepEqual(r,Result{}) || d!=nil { t.Fatal("collector publication cancellation",r,d,err) }
			if _,ok:=err.(interface{SQLState()string});ok { t.Fatal("publication cancellation acquired SQLSTATE") }
			rawAccept(t,rawDocument("ASCII","A"),rawDocument("ASCII","A"),"A",RawInputInfo{Codec:"ASCII"},rawDecl("ASCII"))
		}
	}
}

func TestXMLRawASCIIJP2Limits(t *testing.T) {
	for _,tc:=range []struct { current,additional int; ok bool }{
		{0,0,true},{0x3ffffffe,0,true},{0x3ffffffe,1,false},{0x3fffffff,0,false},{math.MaxInt,1,false},{1,math.MaxInt,false},{-1,1,false},{1,-1,false},
	} { if appendSize(tc.current,tc.additional)!=tc.ok { t.Fatal("checked growth boundary",tc) } }
	limit:=int((^uint(0)>>1)/4)
	if !conversionInputSize(limit-1) || conversionInputSize(limit) || conversionInputSize(math.MaxInt) || conversionInputSize(-1) { t.Fatal("conversion size guard boundary") }
	for _,tc:=range []struct { label,body,text,codec string }{
		{"ASCII",strings.Repeat("a",20000),strings.Repeat("a",20000),"ASCII"},
		{"ISO-2022-JP-2",strings.Repeat("\x1b.A\x1b.F\x1b(B",10000)+"A","A","ISO-2022-JP-2"},
		{"ISO-2022-JP-2","\x1b$(C"+strings.Repeat("GQ",10000)+"\x1b(B",strings.Repeat("한",10000),"ISO-2022-JP-2"},
	} { rawAccept(t,rawDocument(tc.label,tc.body),rawDocument(tc.label,tc.text),tc.text,RawInputInfo{Codec:tc.codec},rawDecl(tc.label)) }
}
