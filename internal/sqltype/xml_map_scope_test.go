package sqltype

import (
	"context"
	"errors"
	"testing"
	"unsafe"
	"stuntgres/internal/xml"
)

func TestXMLMapScopeOwnership(t *testing.T){
	ctx:=context.Background();types,foreign:=testTypes(),testTypes();d:=types[XML]
	one,err:=Integer(types[Int4],1);if err!=nil{t.Fatal(err)}
	row:=[]Value{one,Null(types[Text])};saved:=append([]Value(nil),row...)
	m,err:=NewXMLScopeMapping(ctx,d,xml.DatabaseScope,"d",xml.RowMappingOptions{Nulls:true},nil);if err!=nil{t.Fatal(err)}
	for _,err:=range []error{m.BeginSchema(ctx,"s"),m.BeginTable(ctx,"t",[]string{"a","b"}),m.Append(ctx,row,OutputEnvironment{}),m.EndTable(ctx),m.EndSchema(ctx)}{if err!=nil{t.Fatal(err)}}
	accepted,err:=m.Finish(ctx);if err!=nil||!accepted.ValidResult(d.Usage()){t.Fatal(accepted,err)}
	want:="<d xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\">\n\n<s>\n\n<t>\n\n<row>\n  <a>1</a>\n  <b xsi:nil=\"true\"/>\n</row>\n\n</t>\n\n</s>\n\n</d>\n"
	backing:=accepted.xml;characters:=unsafe.StringData(backing.result.Original());copyValue:=accepted
	retagged,err:=accepted.Retag(foreign[XML]);if err!=nil||retagged.xml!=backing||!SameDatum(copyValue,accepted){t.Fatal("scope retag/copy backing",err)}
	domain,err:=Domain(99020,"scope_domain","scope_domain",d.Usage(),false);if err!=nil{t.Fatal(err)};wrapped,err:=domain.Wrap(accepted);if err!=nil||wrapped.xml!=backing{t.Fatal("scope domain backing",err)}
	for i:=range row{if !SameDatum(row[i],saved[i]){t.Fatal("source row mutated",i)}}
	row[0],err=Integer(types[Int4],99);if err!=nil{t.Fatal(err)};row[1]=String(types[Text],"mutated scratch")
	next,err:=NewXMLScopeMapping(ctx,d,xml.SchemaScope,"next",xml.RowMappingOptions{},nil);if err!=nil{t.Fatal(err)};if err:=next.BeginTable(ctx,"t",[]string{"a","b"});err!=nil{t.Fatal(err)};if err:=next.Append(ctx,row,OutputEnvironment{});err!=nil{t.Fatal(err)};if err:=next.EndTable(ctx);err!=nil{t.Fatal(err)};if _,err:=next.Finish(ctx);err!=nil{t.Fatal(err)}
	assertBacking:=func(){t.Helper();if accepted.xml!=backing||unsafe.StringData(backing.result.Original())!=characters||backing.result.Original()!=want||!SameDatum(accepted,copyValue){t.Fatal("scope backing mutated")}}
	assertBacking();for _,value:=range []Value{retagged,wrapped}{same,err:=accepted.SamePayload(ctx,value);if err!=nil||!same{t.Fatal("shared scope payload",err)}}
	done:=make(chan error,2)
	for range 2{go func(){
		m,err:=NewXMLScopeMapping(ctx,d,xml.SchemaScope,"s",xml.RowMappingOptions{Forest:true},nil);if err==nil{err=m.BeginTable(ctx,"t",[]string{"x"})};if err==nil{err=m.Append(ctx,[]Value{accepted},OutputEnvironment{})};if err==nil{err=m.EndTable(ctx)};var value Value;if err==nil{value,err=m.Finish(ctx)}
		expected:="<s xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\">\n\n<t>\n  <x>"+want+"</x>\n</t>\n\n\n</s>\n"
		if err==nil&&(value.xml==nil||value.xml==backing||value.xml.result.Original()!=expected){err=errors.New("concurrent scope did not own checked output")};done<-err
	}()}
	var failure error;for range 2{if err:=<-done;err!=nil&&failure==nil{failure=err}};if failure!=nil{t.Fatal(failure)};assertBacking()
	failed,err:=NewXMLScopeMapping(ctx,d,xml.SchemaScope,"s",xml.RowMappingOptions{},nil);if err!=nil{t.Fatal(err)};if err:=failed.BeginTable(ctx,"t",[]string{"a"});err!=nil{t.Fatal(err)};if err:=failed.Append(ctx,[]Value{one},OutputEnvironment{});err!=nil{t.Fatal(err)}
	infinite,err:=types[Date].Input(ctx,"infinity",-1);if err!=nil{t.Fatal(err)};if err:=failed.Append(ctx,[]Value{infinite},OutputEnvironment{});err==nil{t.Fatal("infinite typed value admitted")};if value,err:=failed.Finish(ctx);err==nil||value.typ!=nil||value.xml!=nil{t.Fatal("failed scope published",value,err)};assertBacking()
	base,cancel:=context.WithCancel(ctx);cancel();if m,err:=NewXMLScopeMapping(base,d,xml.SchemaScope,"s",xml.RowMappingOptions{},nil);!errors.Is(err,context.Canceled)||m!=nil{t.Fatal("canceled scope",m,err)}
}
