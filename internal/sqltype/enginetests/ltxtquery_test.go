package enginetests

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestLtxtqueryCodec(t *testing.T) {
	ctx:=context.Background();d:=ltxtqueryUnitType(t,65000)
	for _,tc:=range []struct{id sqltype.ID;name,declaration string}{{0,"ltxtquery","public.ltxtquery"},{sqltype.Unknown,"ltxtquery","public.ltxtquery"},{65000,"","public.ltxtquery"},{65000,"ltxtquery",""}} {
		if typ,err:=sqltype.Ltxtquery(tc.id,tc.name,tc.declaration);err==nil || typ!=nil {t.Fatal("invalid identity",tc)}
	}
	for _,tc:=range []struct{input,want string}{
		{" Europe&Russia*@&!Transportation ","Europe & Russia@* & !Transportation"},
		{"foo*@%@@**","foo%@*"},{"((AbC))","AbC"},{"a|b&c","a | b & c"},
		{"(a|b)&c","( a | b ) & c"},{"a|b|c","( a | b ) | c"},{"a|(b|c)","a | ( b | c )"},
		{"a&(b&c)","a & b & c"},{"!!a","!( !a )"},{"!(a|b)","!( a | b )"},{"!(a&b)","!( a & b )"},
		{"\ta &\nb","a & b"},{"0","0"},{"_","_"},{"-","-"},{"a-b_c","a-b_c"},
		{strings.Repeat("(",10000)+"a"+strings.Repeat(")",10000),"a"},
	} {
		v,err:=d.Input(ctx,tc.input,-1);if err!=nil || !v.ValidResult(d.Usage()) {t.Fatal("input",tc.input,err)}
		out,err:=d.Output(ctx,v);if err!=nil || out!=tc.want {t.Fatal("output",out,err,tc.want)}
		literal,err:=d.SQLLiteral(ctx,v);if err!=nil || literal!="'"+tc.want+"'" {t.Fatal("literal",literal,err)}
		again,err:=d.Input(ctx,tc.want,-1);if err!=nil || !sqltype.SameDatum(v,again) {t.Fatal("canonical identity",err)}
		if same,err:=v.SamePayload(ctx,again);err!=nil || !same {t.Fatal("canonical payload",same,err)}
	}
	for _,group:=range []struct{inputs []string;message string}{
		{[]string{""," ","!","a&","()","*","@a","a&&b","é"},"operand syntax error"},
		{[]string{"a b","a.b","a)","(a","a\t","a\n"},"syntax error"},
		{[]string{"foo@bar","foo*bar","foo%bar"},"modifiers syntax error"},
	} {
		for _,input:=range group.inputs { ltxtqueryUnitError(t,d,input,"42601",group.message) }
	}
	for _,at:=range []int{1,3,40,1000} {
		base,stop:=context.WithCancel(ctx)
		periodic:=&lqueryCancelContext{Context:base,cancel:stop,at:at}
		v,err:=d.Input(periodic,strings.Repeat("(",2000)+"a"+strings.Repeat(")",2000),-1);stop()
		if !errors.Is(err,context.Canceled) || v.Type()!=nil || periodic.calls!=at {t.Fatal("periodic cancellation",at,periodic.calls,err)}
	}
	canceled,cancel:=context.WithCancel(ctx);cancel()
	v,_:=d.Input(ctx,"a",-1)
	if _,err:=d.Input(canceled,"a",-1);!errors.Is(err,context.Canceled) {t.Fatal(err)}
	if _,err:=d.Output(canceled,v);!errors.Is(err,context.Canceled) {t.Fatal(err)}
	if _,err:=d.SQLLiteral(canceled,v);!errors.Is(err,context.Canceled) {t.Fatal(err)}
}

func ltxtqueryUnitError(t *testing.T,d *sqltype.Descriptor,input,code,message string) {
	t.Helper();v,err:=d.Input(context.Background(),input,-1)
	var diagnostic *sqlstate.Error
	if v.Type()!=nil || !errors.As(err,&diagnostic) {t.Fatal("failed input returned datum",len(input),err)}
	want:=&sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:code,Message:message}
	if !reflect.DeepEqual(diagnostic,want) {t.Fatalf("diagnostic %#v want %#v",diagnostic,want)}
}

func TestLtxtqueryBoundaries(t *testing.T) {
	d:=ltxtqueryUnitType(t,65000);ctx:=context.Background()
	for _,tc:=range []struct{n int;message string}{{256,"operand is too long"},{65535,"operand is too long"},{65536,"word is too long"}} {
		ltxtqueryUnitError(t,d,strings.Repeat("a",tc.n),"22023",tc.message)
	}
	word:=strings.Repeat("a",255);words:=make([]string,256);for i:=range words {words[i]=word};full:=strings.Join(words,"&")
	for _,tc:=range []struct{input,want string}{
		{word,word},{full,strings.Join(words," & ")},
		{strings.Repeat("!",32)+"a",strings.Repeat("!( ",31)+"!a"+strings.Repeat(" )",31)},
		{strings.Repeat("!",32)+"("+strings.Repeat("!",32)+"a)",strings.Repeat("!( ",63)+"!a"+strings.Repeat(" )",63)},
	} {
		v,err:=d.Input(ctx,tc.input,-1);if err!=nil {t.Fatal(err)}
		out,err:=d.Output(ctx,v);if err!=nil || out!=tc.want {t.Fatal("boundary output",err,len(out),len(tc.want))}
	}
	ltxtqueryUnitError(t,d,full+"&a","22023","value is too big")
	ltxtqueryUnitError(t,d,full+"&"+word+"a","22023","value is too big")
	ltxtqueryUnitError(t,d,full+"&"+strings.Repeat("a",65536),"22023","word is too long")
	ltxtqueryUnitError(t,d,strings.Repeat("!",33)+"a","XX000","stack too short")
	ltxtqueryUnitError(t,d,"a&"+strings.Repeat("!",32)+"b","XX000","stack too short")
	expr:="a";canonical:="a"
	for depth:=1;depth<=14;depth++ {
		expr="("+expr+"&"+expr+")";canonical=canonical+" & "+canonical
		if depth==13 {
			v,err:=d.Input(ctx,"b|"+expr,-1);if err!=nil {t.Fatal(err)}
			out,err:=d.Output(ctx,v);if err!=nil || out!="b | "+canonical {t.Fatal("right offset boundary",err)}
		}
	}
	ltxtqueryUnitError(t,d,"b|"+expr,"54000","ltxtquery is too large")
}
