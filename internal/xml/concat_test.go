package xml

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unsafe"
)

func TestXMLConcatAccumulator(t *testing.T) {
	ctx:=context.Background()
	parse:=func(s string,mode Mode)Result{t.Helper();r,e:=Parse(ctx,s,Options{Mode:mode});if e!=nil{t.Fatal(e)};return r}
	for _,tc:=range []struct{inputs []string;want string;mode Mode}{
		{[]string{""},"",Content},
		{[]string{"<?xml version='1.0' encoding='UTF8'?><a/>"},"<?xml version='1.0' encoding='UTF8'?><a/>",Content},
		{[]string{`<?xml version="1.0"?><a/>`,`<?xml version="1.0"?><b/>`},"<a/><b/>",Content},
		{[]string{`<?xml version="1.1" standalone="yes"?><a/>`,`<?xml version="1.0" standalone="yes"?><b/>`,`<?xml version="1.0" standalone="yes"?><c/>`},`<?xml version="1.0" standalone="yes"?><a/><b/><c/>`,Content},
		{[]string{`<?xml version="1.1"?><a/>`,`<?xml version="1.1"?><b/>`},`<?xml version="1.1"?><a/><b/>`,Content},
		{[]string{"<!DOCTYPE a><a/>","<!DOCTYPE b><b/>"},"<!DOCTYPE a><a/><!DOCTYPE b><b/>",Document},
		{[]string{`<?xml version="1.0" encoding="UTF8"standalone="yes"?><a/>`,"<b/>"},"<b/>",Document},
		{[]string{"\n<a/>","&amp;<b/>"},"\n<a/>&amp;<b/>",Content},
	}{
		var a ConcatAccumulator
		var first Result
		for i,s:=range tc.inputs{r:=parse(s,tc.mode);if i==0{first=r};if err:=a.Append(ctx,r);err!=nil{t.Fatal(err)};if r.Original()!=s{t.Fatal("operand mutated")}}
		r,err:=a.Finish(ctx);if err!=nil||!r.Valid()||r.Original()!=tc.want{t.Fatalf("%q got %q %v",tc.inputs,r.Original(),err)}
		if len(tc.inputs)==1&&unsafe.StringData(r.Original())!=unsafe.StringData(first.Original()){t.Fatal("singleton copied")}
		if len(tc.inputs)>1&&(r.RequestedMode()!=Content||len(r.Diagnostics())!=0){t.Fatal("constructed result metadata",r)}
		old:=r.Original();if err:=a.Append(ctx,parse("<z/>",Content));err!=nil{t.Fatal(err)};if r.Original()!=old{t.Fatal("retained result mutated")}
	}
	var invalid ConcatAccumulator
	if err:=invalid.Append(ctx,Result{});err==nil{t.Fatal("zero accepted")}
	if r,err:=invalid.Finish(ctx);err==nil||r.Valid(){t.Fatal("failed state published")}
	var empty ConcatAccumulator
	if r,err:=empty.Finish(ctx);err==nil||r.Valid(){t.Fatal("unseen accepted")}
	big:=parse("<a>"+strings.Repeat("x",40000)+"</a>",Content)
	headerBig:=parse(`<?xml version="1.1"?>`+big.Original(),Content)
	for _,finish:=range []bool{false,true}{
		var a ConcatAccumulator
		operand:=big
		if finish{operand=headerBig}
		if err:=a.Append(ctx,operand);err!=nil{t.Fatal(err)}
		if finish{if err:=a.Append(ctx,operand);err!=nil{t.Fatal(err)};if !a.declaration.Present{t.Fatal("finalization must emit header")}}
		c:=&phaseContext{Context:ctx,phase:"concatCopy",after:3,cause:context.Canceled}
		if finish{c.after=12} // Admit the emitted header and two body chunks.
		var err error
		if finish{r,e:=a.Finish(c);err=e;if r.Valid(){t.Fatal("partial finish")}}else{err=a.Append(c,big)}
		if !c.admitted||!errors.Is(err,context.Canceled){t.Fatal("copy cancellation",err)}
		if big.Original()!="<a>"+strings.Repeat("x",40000)+"</a>"{t.Fatal("input changed")}
		if headerBig.Original()!=`<?xml version="1.1"?>`+big.Original(){t.Fatal("header operand changed")}
		if _,ok:=err.(interface{SQLState()string});ok{t.Fatal("context acquired SQLSTATE")}
	}
	if appendSize(int(^uint(0)>>1),1){t.Fatal("overflow accepted")}
	c,cancel:=context.WithCancel(ctx);cancel();var a ConcatAccumulator
	if err:=a.Append(c,big);!errors.Is(err,context.Canceled){t.Fatal(err)}
}

func TestXMLConcatAccumulatorGrowth(t *testing.T) {
	ctx:=context.Background()
	parse:=func(s string)Result{t.Helper();r,err:=Parse(ctx,s,Options{Mode:Content});if err!=nil{t.Fatal(err)};return r}
	bigText:="<a>"+strings.Repeat("x",40000)+"</a>"
	big:=parse(bigText)
	small:=parse("<b/>")
	for _,cancelGrowth:=range []bool{false,true}{
		var a ConcatAccumulator
		if err:=a.Append(ctx,big);err!=nil{t.Fatal(err)}
		if err:=a.Append(ctx,big);err!=nil{t.Fatal(err)}
		// Fill the reserved capacity so even the small next operand must grow.
		paddingText:=strings.Repeat("p",a.body.Cap()-a.body.Len())
		padding:=parse(paddingText)
		if err:=a.Append(ctx,padding);err!=nil{t.Fatal(err)}
		retained,err:=a.Finish(ctx);if err!=nil{t.Fatal(err)}
		want:=bigText+bigText+paddingText
		if retained.Original()!=want{t.Fatal("wrong prefix")}
		if unsafe.StringData(retained.Original())!=unsafe.StringData(a.body.String()){t.Fatal("headerless finish copied body")}
		finalCtx:=&phaseContext{Context:ctx,phase:"Finish",after:2,cause:context.Canceled}
		if r,e:=a.Finish(finalCtx);!errors.Is(e,context.Canceled)||r.Valid(){t.Fatal("headerless final fence",e)}
		priorCap:=a.body.Cap()
		growthCtx:=ctx
		var canceled *phaseContext
		if cancelGrowth{
			canceled=&phaseContext{Context:ctx,phase:"reserveConcatBody",after:3,cause:context.Canceled}
			growthCtx=canceled
		}
		err=a.Append(growthCtx,small)
		if cancelGrowth{
			if !canceled.admitted||!errors.Is(err,context.Canceled){t.Fatal("growth cancellation",err)}
			// One chunk of the OLD prefix was copied, not the incoming body.
			if a.body.Len()!=4096||a.body.String()!=want[:4096]{t.Fatal("did not cancel within existing-prefix copy",a.body.Len())}
			if r,e:=a.Finish(ctx);!errors.Is(e,context.Canceled)||r.Valid(){t.Fatal("failed growth published",e)}
			if e:=a.Append(ctx,small);!errors.Is(e,context.Canceled){t.Fatal("failed growth reused",e)}
		}else{
			if err!=nil{t.Fatal(err)}
			if a.body.Cap()<priorCap*2{t.Fatal("growth is not geometric")}
			r,e:=a.Finish(ctx);if e!=nil||r.Original()!=want+"<b/>"{t.Fatal("wrong grown result",e)}
			if unsafe.StringData(r.Original())!=unsafe.StringData(a.body.String()){t.Fatal("grown headerless finish copied body")}
			// An append within spare capacity must also preserve returned strings.
			if err:=a.Append(ctx,small);err!=nil{t.Fatal(err)}
			if r.Original()!=want+"<b/>"{t.Fatal("retained grown result mutated")}
		}
		if retained.Original()!=want||big.Original()!=bigText||padding.Original()!=paddingText||small.Original()!="<b/>"{t.Fatal("retained result or operand mutated")}
	}
	var a ConcatAccumulator
	if err:=a.reserveConcatBody(ctx,int(^uint(0)>>1));err==nil{t.Fatal("unchecked capacity")}
}
