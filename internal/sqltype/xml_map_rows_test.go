package sqltype

import (
	"context"
	"errors"
	"testing"
	"unsafe"
	"stuntgres/internal/xml"
)

func TestXMLMapRowsOwnership(t *testing.T){
	ctx:=context.Background();types,foreign:=testTypes(),testTypes();d:=types[XML]
	one,err:=Integer(types[Int4],1);if err!=nil{t.Fatal(err)}
	row:=[]Value{one,Null(types[Text])};saved:=append([]Value(nil),row...)
	mapper,err:=NewXMLRowMapping(ctx,d,xml.RowMappingOptions{Nulls:true},[]string{"a","b"},nil);if err!=nil{t.Fatal(err)}
	if err:=mapper.Append(ctx,row,OutputEnvironment{});err!=nil{t.Fatal(err)}
	accepted,err:=mapper.Finish(ctx);if err!=nil||!accepted.ValidResult(d.Usage()){t.Fatal(accepted,err)}
	want:="<table xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\">\n\n<row>\n  <a>1</a>\n  <b xsi:nil=\"true\"/>\n</row>\n\n</table>\n"
	backing:=accepted.xml;characters:=unsafe.StringData(backing.result.Original());copyValue:=accepted
	retagged,err:=accepted.Retag(foreign[XML]);if err!=nil||retagged.xml!=backing||!SameDatum(copyValue,accepted){t.Fatal("retag/copy lost accepted backing",err)}
	domain,err:=Domain(99019,"xml_map_domain","xml_map_domain",d.Usage(),false);if err!=nil{t.Fatal(err)};wrapped,err:=domain.Wrap(accepted);if err!=nil||wrapped.xml!=backing{t.Fatal("domain changed backing",err)}
	for i:=range row{if !SameDatum(row[i],saved[i]){t.Fatal("source row mutated",i)}}
	row[0],err=Integer(types[Int4],99);if err!=nil{t.Fatal(err)};row[1]=String(types[Text],"mutated scratch")
	next,err:=NewXMLRowMapping(ctx,d,xml.RowMappingOptions{},[]string{"a","b"},nil);if err!=nil{t.Fatal(err)};if err:=next.Append(ctx,row,OutputEnvironment{});err!=nil{t.Fatal(err)};if _,err:=next.Finish(ctx);err!=nil{t.Fatal(err)}
	if accepted.xml!=backing||unsafe.StringData(backing.result.Original())!=characters||backing.result.Original()!=want||!SameDatum(accepted,copyValue){t.Fatal("later mapping or scratch changed accepted payload")}
	for _,value:=range []Value{accepted,retagged,wrapped}{equal,err:=accepted.SamePayload(ctx,value);if err!=nil||!equal{t.Fatal("shared accepted payload",err)}}
	// Independent invocations share the genuine immutable source payload, never
	// a field vector, builder, output environment or publication buffer.
	done:=make(chan error,2)
	for range 2{go func(){
		m,err:=NewXMLRowMapping(ctx,d,xml.RowMappingOptions{Forest:true},[]string{"x"},nil);if err!=nil{done<-err;return}
		if err=m.Append(ctx,[]Value{accepted},OutputEnvironment{});err!=nil{done<-err;return}
		value,err:=m.Finish(ctx);if err!=nil{done<-err;return}
		expected:="<row xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\">\n  <x>"+want+"</x>\n</row>\n\n"
		if value.xml==nil||value.xml==backing||value.xml.result.Original()!=expected{done<-errors.New("concurrent invocation did not own its output");return}
		done<-nil
	}()}
	var concurrentFailure error;for range 2{if err:=<-done;err!=nil&&concurrentFailure==nil{concurrentFailure=err}}
	if concurrentFailure!=nil{t.Fatal(concurrentFailure)}
	if accepted.xml!=backing||unsafe.StringData(backing.result.Original())!=characters||backing.result.Original()!=want{t.Fatal("concurrent mapping mutated shared input")}
	// Typed conversion failures poison the whole row assembler, not just the
	// failing field; Finish may never publish the earlier successful prefix.
	failed,err:=NewXMLRowMapping(ctx,d,xml.RowMappingOptions{},[]string{"a"},nil);if err!=nil{t.Fatal(err)}
	prefix,err:=Integer(types[Int4],7);if err!=nil{t.Fatal(err)}
	if err:=failed.Append(ctx,[]Value{prefix},OutputEnvironment{});err!=nil{t.Fatal(err)}
	infinite,err:=types[Date].Input(ctx,"infinity",-1);if err!=nil{t.Fatal(err)}
	if err:=failed.Append(ctx,[]Value{infinite},OutputEnvironment{});err==nil{t.Fatal("infinite date mapped")}
	if value,err:=failed.Finish(ctx);err==nil||value.typ!=nil||value.xml!=nil{t.Fatal("failed conversion published",value,err)}
	if value,err:=XMLMappingSchema(ctx,d,xml.MappingSchemaResult{});err==nil||value.xml!=nil{t.Fatal("zero schema token admitted")}
	underlying,cancel:=context.WithCancel(ctx);cancel()
	if m,err:=NewXMLRowMapping(underlying,d,xml.RowMappingOptions{},nil,nil);!errors.Is(err,context.Canceled)||m!=nil{t.Fatal("canceled mapping",m,err)}
	// Source-backed typed fields remain usable after mid-array mapping failure.
	values:=make([]Value,1024);names:=make([]string,len(values));for i:=range values{values[i]=Null(types[Text]);names[i]="f"}
	m,err:=NewXMLRowMapping(ctx,d,xml.RowMappingOptions{},names,nil);if err!=nil{t.Fatal(err)}
	underlying,cancel=context.WithCancel(ctx);defer cancel();c:=&arraySearchCancelContext{Context:underlying,cancel:cancel,remaining:3}
	if err:=m.Append(c,values,OutputEnvironment{});!errors.Is(err,context.Canceled){t.Fatal(err)}
	if value,err:=m.Finish(ctx);!errors.Is(err,context.Canceled)||value.xml!=nil||value.typ!=nil{t.Fatal("canceled row published",value,err)}
	for _,v:=range values{if !v.IsNull()||v.Type()!=types[Text]{t.Fatal("cancellation changed source value")}}
}
