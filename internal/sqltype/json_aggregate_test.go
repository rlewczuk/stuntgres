package sqltype

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"stuntgres/internal/sqlstate"
)

func jsonAggregateUnitPlan(t *testing.T,input,result *Descriptor,absent bool) JSONArrayAggregatePlan {
	t.Helper()
	p,err:=BindJSONArrayAggregate(input.Usage(),result.Usage(),absent)
	if err!=nil {t.Fatal(err)}
	return p
}
func TestJSONArrayAggregatePresenceAndOwnership(t *testing.T) {
	types,foreign:=testTypes(),testTypes()
	ctx:=context.Background()
	for _, id:=range []ID{JSON,JSONB} {
		for _, absent:=range []bool{false,true} {
			p:=jsonAggregateUnitPlan(t,types[Int4],types[id],absent)
			zero,err:=p.Init(ctx,OutputEnvironment{});if err!=nil{t.Fatal(err)}
			v,err:=p.Finalize(ctx,zero);if err!=nil||!v.null||v.typ!=types[id]{t.Fatal("unseen result",v,err)}
			s,err:=p.Init(ctx,OutputEnvironment{});if err!=nil{t.Fatal(err)}
			if err=p.Advance(ctx,s,Null(types[Int4]));err!=nil{t.Fatal(err)}
			v,err=p.Finalize(ctx,s)
			want:="[null]";if absent{want="[]"}
			if err!=nil||v.null||v.text!=want||v.typ!=types[id]{t.Fatal("seen NULL result",v,err)}
			again,err:=p.Finalize(ctx,s);if err!=nil||again!=v{t.Fatal("non-idempotent finalization",again,err)}
			one,_:=Integer(types[Int4],1)
			if err=p.Advance(ctx,s,one);err==nil{t.Fatal("sealed state accepted another value")}
			if v.text!=want{t.Fatal("published output mutated")}
			other,err:=p.Init(ctx,OutputEnvironment{});if err!=nil{t.Fatal(err)}
			if err=p.Advance(ctx,other,one);err!=nil{t.Fatal(err)}
			out,err:=p.Finalize(ctx,other);if err!=nil||out.text!="[1]"||v.text!=want{t.Fatal("group isolation",out,err)}
			foreignValue,_:=Integer(foreign[Int4],1)
			fresh,err:=p.Init(ctx,OutputEnvironment{});if err!=nil{t.Fatal(err)}
			if err=p.Advance(ctx,fresh,foreignValue);err==nil{t.Fatal("foreign generation accepted")}
			if _,err=p.Finalize(ctx,nil);err==nil{t.Fatal("nil state accepted")}
			wrong:=jsonAggregateUnitPlan(t,types[Int4],types[id],!absent)
			if _,err=wrong.Finalize(ctx,fresh);err==nil{t.Fatal("different operation accepted state")}
		}
		p:=jsonAggregateUnitPlan(t,types[JSON],types[id],true)
		j,err:=types[JSON].Input(ctx,"null",-1);if err!=nil{t.Fatal(err)}
		s,err:=p.Init(ctx,OutputEnvironment{});if err!=nil{t.Fatal(err)}
		if err=p.Advance(ctx,s,j);err!=nil{t.Fatal(err)}
		v,err:=p.Finalize(ctx,s);if err!=nil||v.text!="[null]"{t.Fatal("JSON null incorrectly omitted",v,err)}
	}
	for _, bad:=range []Usage{types[Int4].Usage(),{},Usage{Type:types[JSON],Typmod:2}} {
		if _,err:=BindJSONArrayAggregate(types[Int4].Usage(),bad,false);err==nil{t.Fatal("invalid result admitted",bad)}
	}
	if _,err:=BindJSONArrayAggregate(types[JSON].Usage(),foreign[JSON].Usage(),false);err==nil{t.Fatal("foreign JSON result owner admitted")}
}

func TestJSONArrayAggregateStructuredConversion(t *testing.T) {
	types:=testTypes();ctx:=context.Background()
	one,_:=Integer(types[Int4],1);two,_:=Integer(types[Int4],2)
	first,err:=ArrayValue(types[1007],[]ArrayDimension{{Lower:-2,Length:2}},[]Value{one,Null(types[Int4])});if err!=nil{t.Fatal(err)}
	second,err:=ArrayValue(types[1007],[]ArrayDimension{{Lower:5,Length:2}},[]Value{two,two});if err!=nil{t.Fatal(err)}
	shape,err:=RecordShape([]CompositeMember{{Attribute:1,Name:"n",Usage:types[1007].Usage()}});if err!=nil{t.Fatal(err)}
	row,err:=OwnedCompositeValue(shape,[]Value{first});if err!=nil{t.Fatal(err)}
	domain,err:=Domain(91001,"ja_row","ja_row",shape.Usage());if err!=nil{t.Fatal(err)}
	wrapped,err:=domain.Wrap(row);if err!=nil{t.Fatal(err)}
	j,err:=types[JSON].Input(ctx,` {"z":1,"a":1,"a":2} `,-1);if err!=nil{t.Fatal(err)}
	for _, tc:=range []struct{values []Value;json,binary string}{
		{[]Value{first,second},"[[1,null], \n [2,2]]","[[1, null], [2, 2]]"},
		{[]Value{wrapped,wrapped},"[{\"n\":[1,null]}, \n {\"n\":[1,null]}]",`[{"n": [1, null]}, {"n": [1, null]}]`},
		{[]Value{j,j},`[ {"z":1,"a":1,"a":2} ,  {"z":1,"a":1,"a":2} ]`,`[{"a": 2, "z": 1}, {"a": 2, "z": 1}]`},
		{[]Value{String(types[Text],"é\"\\\n")},`["é\"\\\n"]`,`["é\"\\\n"]`},
	} {
		for _, id:=range []ID{JSON,JSONB} {
			p:=jsonAggregateUnitPlan(t,tc.values[0].typ,types[id],false)
			s,err:=p.Init(ctx,OutputEnvironment{});if err!=nil{t.Fatal(err)}
			for _, value:=range tc.values {if err=p.Advance(ctx,s,value);err!=nil{t.Fatal(err)}}
			v,err:=p.Finalize(ctx,s);want:=tc.json;if id==JSONB{want=tc.binary}
			if err!=nil||v.text!=want||v.typ!=types[id]||id==JSONB&&v.jsonb==nil{t.Fatalf("conversion got %q want %q err=%v",v.text,want,err)}
		}
	}
	if row.CompositeField(0).array!=first.array||first.ArrayDimension(0).Lower!=-2{t.Fatal("borrowed input changed")}
}

func TestJSONArrayAggregateSQLJSONCoercion(t *testing.T) {
	types,next:=testTypes(),testTypes();ctx:=context.Background()
	for _, id:=range []ID{Text,Bytea,VarChar,BPChar,Name,JSON,JSONB} {
		u:=types[id].Usage();if id==VarChar||id==BPChar{u.Typmod=7}
		p,err:=PlanJSONReturning(types[JSON].Usage(),u);if err!=nil{t.Fatal(err)}
		v,err:=types[JSON].Input(ctx,"[1]",-1);if err!=nil{t.Fatal(err)}
		out,err:=p.Apply(ctx,v,InputEnvironment{},OutputEnvironment{});if err!=nil||out.typ!=types[id]||out.text!="[1]"{t.Fatal(id,out,err)}
		mapped,err:=p.Rebind(next[JSON].Usage(),Usage{Type:next[id],Typmod:u.Typmod,Collation:u.Collation},Assignment);if err!=nil{t.Fatal(err)}
		other,err:=next[JSON].Input(ctx,"[1]",-1);if err!=nil{t.Fatal(err)}
		if out,err=mapped.Apply(ctx,other,InputEnvironment{},OutputEnvironment{});err!=nil||out.typ!=next[id]{t.Fatal("coercion remap",out,err)}
		if _,err=mapped.Apply(ctx,v,InputEnvironment{},OutputEnvironment{});err==nil{t.Fatal("remapped coercion accepted old datum")}
	}
	p,err:=PlanJSONReturning(types[JSON].Usage(),Usage{Type:types[VarChar],Typmod:6,Collation:types[VarChar].Usage().Collation});if err!=nil{t.Fatal(err)}
	v,_:=types[JSON].Input(ctx,"[1]",-1)
	_,err=p.Apply(ctx,v,InputEnvironment{},OutputEnvironment{})
	var diagnostic *sqlstate.Error
	if !errors.As(err,&diagnostic)||diagnostic.Code!="22001"{t.Fatal("assignment truncated JSON",err)}
	for _, id:=range []ID{Text,Bytea} {
		p,err:=PlanJSONFormattedInput(types[id].Usage(),types[JSON].Usage(),"UTF8");if err!=nil{t.Fatal(err)}
		valid,bad:=String(types[Text],"[1]"),String(types[Text],"no")
		if id==Bytea {valid,err=Binary(types[Bytea],"[1]");if err!=nil{t.Fatal(err)};bad,err=Binary(types[Bytea],"no");if err!=nil{t.Fatal(err)}}
		out,err:=p.Apply(ctx,valid,InputEnvironment{},OutputEnvironment{});if err!=nil||out.text!="[1]"||out.typ!=types[JSON]{t.Fatal(out,err)}
		_,err=p.Apply(ctx,bad,InputEnvironment{},OutputEnvironment{})
		if !errors.As(err,&diagnostic)||diagnostic.Code!="22P02"||diagnostic.Position!=""||diagnostic.Context!="JSON data, line 1: no"{t.Fatal("formatted codec diagnostic",err)}
	}
	for _, encoding:=range []string{"UTF16","UTF32"} {
		p,err:=PlanJSONFormattedInput(types[Bytea].Usage(),types[JSON].Usage(),encoding);if err!=nil{t.Fatal(err)}
		input,err:=Binary(types[Bytea],"[1]");if err!=nil{t.Fatal(err)}
		_,err=p.Apply(ctx,input,InputEnvironment{},OutputEnvironment{})
		if !errors.As(err,&diagnostic)||diagnostic.Code!="22023"||diagnostic.Message!=`invalid source encoding name "`+encoding+`"`||diagnostic.Position!=""{t.Fatal("input encoding diagnostic",err)}
	}
	for _, tc:=range []struct{raw,message string}{
		{"\xff",`invalid byte sequence for encoding "UTF8": 0xff`},
		{"\x00",`invalid byte sequence for encoding "UTF8": 0x00`},
	} {
		p,err:=PlanJSONFormattedInput(types[Bytea].Usage(),types[JSON].Usage(),"UTF8");if err!=nil{t.Fatal(err)}
		input,err:=Binary(types[Bytea],tc.raw);if err!=nil{t.Fatal(err)}
		_,err=p.Apply(ctx,input,InputEnvironment{},OutputEnvironment{})
		if !errors.As(err,&diagnostic)||diagnostic.Code!="22021"||diagnostic.Message!=tc.message||diagnostic.Context!=""||diagnostic.Position!=""{t.Fatal("formatted BYTEA skipped UTF8 conversion",err)}
	}
}

type jsonAggregateLoopContext struct {
	context.Context
	cancel context.CancelFunc
	target string
	entered bool
}
func (c *jsonAggregateLoopContext) Err() error {
	if !c.entered {
		var pcs [40]uintptr
		frames:=runtime.CallersFrames(pcs[:runtime.Callers(2,pcs[:])])
		for {frame,more:=frames.Next();if strings.HasSuffix(frame.Function,c.target){c.entered=true;c.cancel();break};if !more{break}}
	}
	return c.Context.Err()
}
func TestJSONArrayAggregateReachedLoopCancellation(t *testing.T) {
	types:=testTypes();base:=context.Background()
	for _, tc:=range []struct{id ID;target string;final bool}{
		{JSON,".writeJSONBString",false},{JSONB,".jsonbOutput",true},
	} {
		p:=jsonAggregateUnitPlan(t,types[Text],types[tc.id],false)
		s,err:=p.Init(base,OutputEnvironment{});if err!=nil{t.Fatal(err)}
		ctx,cancel:=context.WithCancel(base)
		probe:=&jsonAggregateLoopContext{Context:ctx,cancel:cancel,target:tc.target}
		value:=String(types[Text],"é\"\\")
		if tc.final {
			if err=p.Advance(base,s,value);err!=nil{cancel();t.Fatal(err)}
			out,failure:=p.Finalize(probe,s)
			if !probe.entered||!errors.Is(failure,context.Canceled)||out!=(Value{}){cancel();t.Fatal("output loop cancellation",probe.entered,out,failure)}
		} else {
			if err=p.Advance(probe,s,value);!probe.entered||!errors.Is(err,context.Canceled){cancel();t.Fatal("conversion loop cancellation",probe.entered,err)}
		}
		cancel()
		if out,err:=p.Finalize(base,s);!errors.Is(err,context.Canceled)||out!=(Value{}){t.Fatal("failed state published partial output",out,err)}
	}
}
