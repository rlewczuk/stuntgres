package enginetests

import (
	"context"
	"errors"
	"testing"
	"stuntgres/internal/sqltype"
)

func TestXMLAggregateValueOwnership(t *testing.T){
	ctx:=context.Background();types:=txidSnapshotTypes();d:=types[sqltype.XML]
	input:=func(s string)sqltype.Value{t.Helper();v,e:=sqltype.XMLInput(ctx,d,s,sqltype.XMLContent,sqltype.InputEnvironment{});if e!=nil{t.Fatal(e)};return v}
	a,e:=sqltype.NewXMLAggregateValue(d);if e!=nil{t.Fatal(e)}
	v,e:=a.Finalize(ctx);if e!=nil||!v.IsNull()||v.Type()!=d{t.Fatal(v,e)}
	first:=input("<?xml version='1.0' encoding='UTF8'?><a/>")
	for _,v:=range []sqltype.Value{sqltype.Null(d),first,sqltype.Null(d)}{if e:=a.Advance(ctx,v);e!=nil{t.Fatal(e)}}
	v,e=a.Finalize(ctx);if e!=nil||!sqltype.SameDatum(v,first){t.Fatal("singleton backing changed",e)}
	if e:=a.Advance(ctx,input("<b/>"));e!=nil{t.Fatal(e)}
	old,e:=a.Finalize(ctx);if e!=nil{t.Fatal(e)};r,e:=old.XML();if e!=nil||!r.Valid()||r.Original()!="<a/><b/>"{t.Fatal(r,e)}
	if !old.ValidResult(d.Usage()){t.Fatal("constructed XML rejected by typed result boundary")}
	if e:=a.Advance(ctx,input("<c/>"));e!=nil{t.Fatal(e)}
	if r.Original()!="<a/><b/>"{t.Fatal("retained backing mutated")}
	fresh,e:=a.Finalize(ctx);if e!=nil{t.Fatal(e)};fr,_:=fresh.XML();if fr.Original()!="<a/><b/><c/>"{t.Fatal(fr)}
	if _,e:=sqltype.NewXMLAggregateValue(types[sqltype.Text]);e==nil{t.Fatal("non XML accepted")}
	other:=txidSnapshotTypes()[sqltype.XML]
	if e:=a.Advance(ctx,sqltype.Null(other));e==nil{t.Fatal("foreign descriptor accepted")}
	if e:=a.Advance(ctx,sqltype.Value{});e==nil{t.Fatal("zero accepted")}
	b,e:=sqltype.NewXMLAggregateValue(d);if e!=nil{t.Fatal(e)}
	if e:=b.Advance(ctx,input(""));e!=nil{t.Fatal(e)};v,e=b.Finalize(ctx);if e!=nil||v.IsNull(){t.Fatal("empty became NULL",e)}
	c,cancel:=context.WithCancel(ctx);cancel();v,e=a.Finalize(c);if !errors.Is(e,context.Canceled)||v.Type()!=nil{t.Fatal("canceled output",v,e)}
	v,e=a.Finalize(ctx);if e!=nil{t.Fatal("healthy reuse",e)}
}
