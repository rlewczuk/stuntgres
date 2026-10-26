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

func lqueryUnitType(t *testing.T, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	d, err := sqltype.Lquery(id, "lquery", "public.lquery")
	if err != nil { t.Fatal(err) }
	return d
}

func TestLqueryCodec(t *testing.T) {
	ctx := context.Background()
	d := lqueryUnitType(t, 65000)
	for _, tc := range []struct { id sqltype.ID; name, declaration string }{
		{0,"lquery","public.lquery"}, {sqltype.Unknown,"lquery","public.lquery"}, {65000,"","public.lquery"}, {65000,"lquery",""},
	} {
		if typ, err := sqltype.Lquery(tc.id, tc.name, tc.declaration); err == nil || typ != nil { t.Fatal("invalid descriptor accepted", tc) }
	}
	for _, tc := range []struct { input, want string }{
		{"Top.*{0,2}.sport*@.!football|tennis{1,}.Russ*|Spain", "Top.*{,2}.sport@*.!football|tennis{1,}.Russ*|Spain"},
		{"foo*@%@@**", "foo%@*"}, {"!Foo@|Bar%*{02,004}", "!Foo@|Bar%*{2,4}"},
		{"foo","foo"}, {"foo{1}","foo{1}"}, {"foo{1,1}","foo{1}"}, {"foo{0,65535}","foo{,}"}, {"foo{2,65535}","foo{2,}"},
		{"*","*"}, {"*{,}","*"}, {"*{0,65535}","*"}, {"*{0}","*{0}"}, {"*{65535,}","*{65535}"},
		{"a{0,2}","a{,2}"}, {"a{,2}","a{,2}"}, {"a{,}","a{,}"}, {"a{2,}","a{2,}"},
		{"42._.-.a-b_c","42._.-.a-b_c"}, {"b|a|b","b|a|b"}, {"4|3|2","4|3|2"}, {"*.1.4|3|2.*","*.1.4|3|2.*"}, {"1.*.4|3|2.*{,4}","1.*.4|3|2.*{,4}"},
		{"a{4294967296}","a{0}"}, {"a{4294967297}","a{1}"}, {"a{0,4294967297}","a{,1}"},
		{strings.Repeat("a",1000)+"*@%",strings.Repeat("a",1000)+"%@*"},
		{strings.Repeat("a.",65534)+"a",strings.Repeat("a.",65534)+"a"},
		{strings.Repeat("a|",4093)+"a",strings.Repeat("a|",4093)+"a"},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil || v.Type() != d || v.IsNull() || !v.ValidResult(d.Usage()) { t.Fatalf("input length %d: %v",len(tc.input),err) }
		out, err := d.Output(ctx,v)
		if err != nil || out != tc.want { t.Fatalf("output length %d: %q/%v want %q",len(tc.input),out,err,tc.want) }
		literal, err := d.SQLLiteral(ctx,v)
		if err != nil || literal != "'"+tc.want+"'" { t.Fatal("literal",literal,err) }
		again, err := d.Input(ctx,tc.want,-1)
		if err != nil || !sqltype.SameDatum(v,again) { t.Fatal("canonical idempotence",err) }
		if same, err := v.SamePayload(ctx,again); err != nil || !same { t.Fatal("canonical payload",same,err) }
	}
	for _, tc := range []struct { input, code, message, detail string }{
		{"","42601","lquery syntax error","Unexpected end of input."}, {"a.","42601","lquery syntax error","Unexpected end of input."},
		{"a|","42601","lquery syntax error","Unexpected end of input."}, {"a{","42601","lquery syntax error","Unexpected end of input."},
		{".a","42601","lquery syntax error at character 1",""}, {"a..b","42601","lquery syntax error at character 3",""}, {"a||b","42601","lquery syntax error at character 3",""},
		{"a b","42601","lquery syntax error at character 2",""}, {" ","42601","lquery syntax error at character 1",""}, {"\t","42601","lquery syntax error at character 1",""}, {"\n","42601","lquery syntax error at character 1",""},
		{"foo@bar","42601","lquery syntax error at character 5",""}, {"a{}","42601","lquery syntax error at character 3",""},
		{"a{2,1}","42601","lquery syntax error","Low limit (2) is greater than high limit (1), at character 5."},
		{"a{65536}","54000","lquery syntax error","Low limit (65536) exceeds the maximum allowed (65535), at character 3."},
		{"a{1,65536}","54000","lquery syntax error","High limit (65536) exceeds the maximum allowed (65535), at character 5."},
		{"!","42601","lquery syntax error at character 2","Empty labels are not allowed."},
		{strings.Repeat("a",1001),"42622","label string is too long","Label length is 1001, must be at most 1000, at character 1002."},
		{strings.Repeat("a",1001)+"@%*","42622","label string is too long","Label length is 1001, must be at most 1000, at character 1002."},
		{strings.Repeat("a.",65535)+"a","54000","number of lquery items (65536) exceeds the maximum allowed (65535)",""},
		{"!"+strings.Repeat(".",65535),"54000","number of lquery items (65536) exceeds the maximum allowed (65535)",""},
		{strings.Repeat("a|",4094)+"a","54000","lquery level is too large","Total size of level exceeds the maximum allowed (65535 bytes)."},
		{strings.Repeat("a|",65535)+"a","54000","lquery level has too many variants","Number of variants exceeds the maximum allowed (65535)."},
		{"é","42601","lquery syntax error at character 1",""}, {"a.é","42601","lquery syntax error at character 3",""},
		{"a{2147483648}","54000","lquery syntax error","Low limit (-2147483648) exceeds the maximum allowed (65535), at character 3."},
		{"a{"+strings.Repeat("9",40)+"}","54000","lquery syntax error","Low limit (-1) exceeds the maximum allowed (65535), at character 3."},
		{"a{1}x","42601","lquery syntax error at character 5",""}, {"|a","42601","lquery syntax error at character 1",""}, {"@","42601","lquery syntax error at character 1",""},
		{"a\x00","22021",`invalid byte sequence for encoding "UTF8": 0x00`,""}, {"a\xff","22021",`invalid byte sequence for encoding "UTF8": 0xff`,""},
	} {
		v, err := d.Input(ctx,tc.input,-1)
		var diagnostic *sqlstate.Error
		if v.Type() != nil || !errors.As(err,&diagnostic) { t.Fatal("failed input returned datum",len(tc.input),v,err) }
		want := &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:tc.code,Message:tc.message,Detail:tc.detail}
		if !reflect.DeepEqual(diagnostic,want) { t.Fatalf("diagnostic input length %d: %#v want %#v",len(tc.input),diagnostic,want) }
	}
}

func TestLqueryIdentity(t *testing.T) {
	ctx := context.Background()
	d, foreign, destination := lqueryUnitType(t,65000), lqueryUnitType(t,65000), lqueryUnitType(t,65001)
	if !d.IsLquery() || !d.IsUserDefined() || d.IsBuiltin() || !d.Concrete() || d.DatabaseName() != "LQUERY" { t.Fatal("scalar identity") }
	facts, ok := d.PGTypeMetadata()
	if !ok || facts.Length != -1 || facts.ByValue || facts.Alignment != "i" || facts.Storage != "x" || facts.Kind != "b" || facts.Category != "U" || facts.Modifier != -1 || facts.Collation != 0 { t.Fatal("catalog facts",facts,ok) }
	v, err := d.Input(ctx,"foo*@%",-1)
	if err != nil { t.Fatal(err) }
	for _, value := range []sqltype.Value{v,sqltype.Null(d)} {
		if same, err := value.Retag(d); err != nil || same != value { t.Fatal("same generation",err) }
		for _, target := range []*sqltype.Descriptor{foreign,destination} {
			if _, err := value.Retag(target); err == nil { t.Fatal("foreign retag admitted") }
		}
		mapped, err := value.RemapLquery(destination)
		if err != nil || mapped.Type() != destination || mapped.IsNull() != value.IsNull() { t.Fatal("authorized remap",err) }
		back, err := mapped.RemapLquery(d)
		if err != nil || back != value { t.Fatal("remap copied immutable payload",err) }
	}
	var invalid sqltype.Value
	if invalid.ValidResult(d.Usage()) { t.Fatal("missing payload admitted") }
	if _, err := invalid.Retag(d); err == nil { t.Fatal("invalid datum retagged") }
	if _, err := invalid.RemapLquery(d); err == nil { t.Fatal("invalid datum imported") }
	if _, err := d.Output(ctx,invalid); err == nil { t.Fatal("invalid payload output") }
	other, _ := d.Input(ctx,"bar",-1)
	if sqltype.SameDatum(v,other) { t.Fatal("distinct canonical bytes compare equal") }
	if same, err := v.SamePayload(ctx,other); err != nil || same { t.Fatal("distinct payload",same,err) }
	if equal, order := sqltype.ComparisonCapabilities(d); equal || order { t.Fatal("storage granted SQL comparison") }
	if _, err := sqltype.Array(65002,"_lquery",d); err == nil { t.Fatal("storage granted executable array") }
	implicit, err := d.Input(ctx,"foo",-1)
	if err != nil { t.Fatal(err) }
	explicit, err := d.Input(ctx,"foo{1}",-1)
	if err != nil { t.Fatal(err) }
	if sqltype.SameDatum(implicit,explicit) { t.Fatal("explicit non-star quantifier discarded") }
	if same, err := implicit.SamePayload(ctx,explicit); err != nil || same { t.Fatal("explicit quantifier payload",same,err) }
}

type lqueryCancelContext struct { context.Context; cancel context.CancelFunc; calls int; at int }
func (c *lqueryCancelContext) Err() error {
	c.calls++
	if c.calls == c.at { c.cancel() }
	return c.Context.Err()
}

func TestLqueryCancellation(t *testing.T) {
	d := lqueryUnitType(t,65000)
	v, err := d.Input(context.Background(),"foo%@*",-1)
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithCancel(context.Background()); cancel()
	if _, err := d.Input(ctx,"a",-1); !errors.Is(err,context.Canceled) { t.Fatal("input cancellation",err) }
	if _, err := d.Output(ctx,v); !errors.Is(err,context.Canceled) { t.Fatal("output cancellation",err) }
	if _, err := d.SQLLiteral(ctx,v); !errors.Is(err,context.Canceled) { t.Fatal("literal cancellation",err) }
	for _, tc := range []struct { text string; at int }{
		{strings.Repeat("a.",4096)+"a",3},
		{strings.Repeat("a.",4096)+"a",40},
		{"a{"+strings.Repeat("0",8192)+"1}",40},
		{"a"+strings.Repeat("@",8192),40},
	} {
		base, stop := context.WithCancel(context.Background())
		periodic := &lqueryCancelContext{Context:base,cancel:stop,at:tc.at}
		value, err := d.Input(periodic,tc.text,-1)
		stop()
		if !errors.Is(err,context.Canceled) || value.Type() != nil || periodic.calls != tc.at { t.Fatal("periodic cancellation",periodic.calls,err) }
	}
}

func TestLqueryCanonicalAllocation(t *testing.T) {
	d := lqueryUnitType(t, 65000)
	ctx := context.Background()
	for _, tc := range []struct{ name, input, want string }{
		{"leading zeros", "a{" + strings.Repeat("0", 65536) + "1}", "a{1}"},
		{"repeated modifiers", "a" + strings.Repeat("@", 65536), "a@"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var retained sqltype.Value
			result := testing.Benchmark(func(b *testing.B) {
				for range b.N {
					var err error
					retained, err = d.Input(ctx, tc.input, -1)
					if err != nil { b.Fatal(err) }
				}
			})
			got, err := d.Output(ctx, retained)
			if err != nil || got != tc.want { t.Fatal("canonical result", got, err) }
			// Calibrated per-operation accounting excludes the caller-owned input.
			// Compact output must not reserve storage for discarded spelling.
			if bytes := result.AllocedBytesPerOp(); bytes > 1024 {
				t.Fatalf("compact canonical value allocated %d bytes, want at most 1024", bytes)
			}
		})
	}
}
