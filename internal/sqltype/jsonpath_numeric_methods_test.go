package sqltype

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestJSONPathNumericMethodsNumeric(t *testing.T) {
	types := testTypes()
	plan := navigatePlan(t,types)
	fresh:=testTypes()
	for _, method:=range []string{"abs","floor","ceiling","number","double"} {
		values:=navigateValues(t,types,`1.25`,`$.`+method+`()`,`{}`,true)
		for slot:=range 4 {
			copy:=append([]Value(nil),values...)
			v,err:=copy[slot].Retag(fresh[copy[slot].typ.ID()])
			if err!=nil {t.Fatal(err)}
			copy[slot]=v
			if _,err:=plan.Evaluate(context.Background(),copy,JSONPathEnvironment{});err==nil {t.Fatal("foreign provider accepted",method,slot)}
		}
	}
	for _, tc := range []struct{doc,path,want string}{
		{`[-0.00,-1.2300,9007199254740993]`,`$[*].abs()`,`[0.00, 1.2300, 9007199254740993]`},
		{`[-0.0001,1.000,12345678901234567890.1]`,`$[*].floor()`,`[-1, 1, 12345678901234567890]`},
		{`[-1.1,0.0001,12345678901234567890.1]`,`$[*].ceiling()`,`[-1, 1, 12345678901234567891]`},
		{`[9007199254740993,"9007199254740993",1.2300,"1.2300"]`,`$[*].number()`,`[9007199254740993, 9007199254740993, 1.2300, 1.2300]`},
		{`[9007199254740993,"9007199254740993",1.2300,"1.2300"]`,`$[*].double()`,`[9007199254740993, 9007199254740990, 1.2300, 1.23]`},
		{`"1.234567890123456789"`,`$.double()`,`[1.23456789012346]`},
	} {
		values := navigateValues(t,types,tc.doc,tc.path,`{}`,false)
		before := values[0].jsonb.root
		got,err := plan.Evaluate(context.Background(),values,JSONPathEnvironment{})
		if err != nil || got.text != tc.want { t.Fatal(tc.path,got.text,err,tc.want) }
		if !reflect.DeepEqual(before,values[0].jsonb.root) { t.Fatal("mutated operand") }
	}
	for _, method := range []string{"ceiling","floor"} {
		doc := strings.Repeat("9",131072)+".1"
		if method == "floor" { doc = "-"+doc }
		values := navigateValues(t,types,doc,"$."+method+"()",`{}`,true)
		_,err := plan.Evaluate(context.Background(),values,JSONPathEnvironment{})
		var state interface{SQLState() string}
		if !errors.As(err,&state) || state.SQLState() != "22003" { t.Fatal("arithmetic overflow must remain ordinary",err) }
	}
}

func TestJSONPathNumericMethodsFloatInput(t *testing.T) {
	for _,tc := range []struct{source string;want float64}{
		{"0x10",16},{"0X10  ",16},{"0x1.8p1",3},{"-0x1.8p1",-3},{"0x.8",0.5},{"+0X1P+4",16},{"0x0p-9999",0},{"0x1p-1074",math.SmallestNonzeroFloat64},{" 1.25 ",1.25},
	} {
		v,err := jsonpathDoubleInput(context.Background(),tc.source)
		if err != nil || v.floating != tc.want || v.typ.id != Float8 { t.Fatal(tc.source,v.floating,err) }
	}
	for _,source := range []string{"NaN(payload)","NaN()","+NaN","nan(foo_bar)","-NaN(foo)"} {
		v,err := jsonpathDoubleInput(context.Background(),source)
		if err != nil || !math.IsNaN(v.floating) { t.Fatal(source,v.floating,err) }
	}
	for _,source := range []string{"0x","0x.p1","0x1p","0x1p+","0x1_0","0x1p2junk","0x1p1024","0x1p-1075","NaN(x-y)","NaN(foo)junk","1_000","1e-400"} {
		if _,err := jsonpathDoubleInput(context.Background(),source); err == nil { t.Fatal("accepted invalid",source) }
	}
}

func TestJSONPathNumericMethodsOwnership(t *testing.T) {
	types := testTypes()
	plan := navigatePlan(t,types)
	for _,method := range []string{"abs","floor","ceiling","number","double"} {
		values := navigateValues(t,types,`[-1.2300,9007199254740993]`,`$[*].`+method+`()`,`{}`,false)
		root,path := values[0].jsonb,values[1].jsonpath
		ast := *path.root.next.next
		var nodes []*jsonbNode
		_,err := executeJSONPath(context.Background(),values[0],values[1],values[2],JSONPathEnvironment{},func(item jsonpathItem)(bool,error){ nodes=append(nodes,item.jsonbNode);return true,nil })
		if err != nil || len(nodes)!=2 { t.Fatal(err,len(nodes)) }
		var retained []string
		for _,node := range nodes { s,err := jsonbOutput(context.Background(),node);if err!=nil {t.Fatal(err)};retained=append(retained,s) }
		if method=="number" || method=="double" {
			if nodes[0]!=&root.root.items[0] || nodes[1]!=&root.root.items[1] { t.Fatal("identity numeric branch unnecessarily replaces backing") }
		}
		for range 3 {
			bad:=append([]Value(nil),values...)
			bad[0]=jsonbSetUnitInput(t,types[JSONB],`[1,"bad",2]`)
			if _,err:=plan.Evaluate(context.Background(),bad,JSONPathEnvironment{});err==nil {t.Fatal("missing failure")}
			bad[3]=Boolean(types[Bool],true)
			got,err:=plan.Evaluate(context.Background(),bad,JSONPathEnvironment{})
			if err!=nil || got.text!=`[1]` {t.Fatal(got.text,err)}
		}
		if values[0].jsonb!=root || values[1].jsonpath!=path || !reflect.DeepEqual(ast,*path.root.next.next) {t.Fatal("accepted backing changed")}
		for i,node:=range nodes {got,err:=jsonbOutput(context.Background(),node);if err!=nil || got!=retained[i] {t.Fatal("retained output changed",got,err)}}
	}
}

func TestJSONPathNumericMethodsIsolation(t *testing.T) {
	types:=testTypes()
	plan:=navigatePlan(t,types)
	for _,method:=range []string{"abs","floor","ceiling","number","double"} {
		path:=jsonbSetUnitInput(t,types[JSONPath],`$.`+method+`() `)
		inputs:=[]Value{jsonbSetUnitInput(t,types[JSONB],`1.25`),jsonbSetUnitInput(t,types[JSONB],`-2.35`),jsonbSetUnitInput(t,types[JSONB],`"bad"`)}
		vars:=jsonbSetUnitInput(t,types[JSONB],`{}`)
		wants:=map[string][]string{"abs":{`[1.25]`,`[2.35]`},"floor":{`[1]`,`[-3]`},"ceiling":{`[2]`,`[-2]`},"number":{`[1.25]`,`[-2.35]`},"double":{`[1.25]`,`[-2.35]`}}
		var wg sync.WaitGroup
		for worker:=range 12 {wg.Go(func(){
			i:=worker%3
			for iteration:=range 20 {
				silent:=iteration%2==0
				got,err:=plan.Evaluate(context.Background(),[]Value{inputs[i],path,vars,Boolean(types[Bool],silent)},JSONPathEnvironment{})
				if i==2 {if silent {if err!=nil || got.text!=`[]` {t.Error(got.text,err)}} else if err==nil {t.Error("missing error")}} else if err!=nil || got.text!=wants[method][i] {t.Error("cross-execution state",got.text,err)}
			}
		})}
		wg.Wait()
	}
}

func TestJSONPathNumericMethodsCancellation(t *testing.T) {
	types:=testTypes()
	plan:=navigatePlan(t,types)
	for _, source:=range []string{"0x"+strings.Repeat("1",jsonbWorkChunk*3),"NaN("+strings.Repeat("a",jsonbWorkChunk*3)+")",strings.Repeat(" ",jsonbWorkChunk*3)+"0x1"} {
		ctx:=&groupingCancelContext{Context:context.Background(),at:2}
		if _,err:=jsonpathDoubleInput(ctx,source);!errors.Is(err,context.Canceled) {t.Fatal("adapter scan cancellation",err)}
	}
	ctx,cancel:=context.WithCancel(context.Background());cancel()
	for _,method:=range []string{"abs","floor","ceiling","number","double"} {
		values:=navigateValues(t,types,`1.25`,`$.`+method+`()`,`{}`,true)
		_,err:=plan.Evaluate(ctx,values,JSONPathEnvironment{})
		if !errors.Is(err,context.Canceled) {t.Fatal(method,err)}
		e:=jsonpathExecutor{ctx:ctx}
		_,err=e.numericMethod(values[1].jsonpath.root.next,&values[0].jsonb.root)
		if !errors.Is(err,context.Canceled) {t.Fatal(method,err)}
	}
	if _,err:=jsonpathDoubleInput(ctx,"0x"+strings.Repeat("1",jsonbWorkChunk*2));!errors.Is(err,context.Canceled) {t.Fatal(err)}
}
