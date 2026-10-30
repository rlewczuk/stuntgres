package functions

import (
	"context"
	"errors"
	"testing"
	"stuntgres/internal/sqltype"
)

func TestXMLAggregateProvider(t *testing.T){
	ctx:=context.Background();d,_:=aggregateUnitType(sqltype.XML);p:=NewXMLAggregateProvider(d.Usage())
	if p.ResultUsage()!=d.Usage(){t.Fatal("wrong result usage")}
	if _,ok:=any(p).(PartialAggregateProvider);ok{t.Fatal("partial capability")}
	if _,ok:=any(p).(AggregateStateTransport);ok{t.Fatal("transport capability")}
	s,err:=p.Init(ctx);if err!=nil{t.Fatal(err)};isolated,err:=p.Init(ctx);if err!=nil{t.Fatal(err)}
	input:=func(text string)sqltype.Value{v,e:=sqltype.XMLInput(ctx,d,text,sqltype.XMLContent,sqltype.InputEnvironment{});if e!=nil{t.Fatal(e)};return v}
	for _,v:=range []sqltype.Value{sqltype.Null(d),input(""),input("<a/>"),sqltype.Null(d),input("<b/>")}{if err:=p.Advance(ctx,s,[]sqltype.Value{v},nil);err!=nil{t.Fatal(err)}}
	v,err:=p.Finalize(ctx,s);if err!=nil{t.Fatal(err)};r,err:=v.XML();if err!=nil||r.Original()!="<a/><b/>"{t.Fatal(r,err)}
	v,err=p.Finalize(ctx,isolated);if err!=nil||!v.IsNull()||v.Type()!=d{t.Fatal("group leaked",v,err)}
	for _,bad:=range []any{nil,struct{}{},(*sqltype.XMLAggregateValue)(nil)}{if _,err:=p.Finalize(ctx,bad);err==nil{t.Fatal("state accepted")};if err:=p.Advance(ctx,bad,[]sqltype.Value{input("<a/>")},nil);err==nil{t.Fatal("state accepted")}}
	for _,vector:=range [][]sqltype.Value{nil,{input("<a/>"),input("<b/>")},{sqltype.Value{}}}{if err:=p.Advance(ctx,s,vector,nil);err==nil{t.Fatal("invalid vector accepted")}}
	foreign:=newRemapGenerations(t).target[sqltype.XML]
	foreignState,err:=NewXMLAggregateProvider(foreign.Usage()).Init(ctx);if err!=nil{t.Fatal(err)}
	if err:=p.Advance(ctx,foreignState,[]sqltype.Value{input("<a/>")},nil);err==nil{t.Fatal("foreign state accepted")}
	c,cancel:=context.WithCancel(ctx);cancel()
	if state,err:=p.Init(c);!errors.Is(err,context.Canceled)||state!=nil{t.Fatal(state,err)}
	if v,err:=p.Finalize(c,s);!errors.Is(err,context.Canceled)||v.Type()!=nil{t.Fatal(v,err)}
	if err:=p.Advance(c,s,[]sqltype.Value{input("<a/>")},nil);!errors.Is(err,context.Canceled){t.Fatal(err)}
	if _,err:=p.Finalize(ctx,s);err!=nil{t.Fatal("healthy state lost",err)}
}
