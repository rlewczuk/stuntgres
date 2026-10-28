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

func ltreeUnitType(t *testing.T, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	d, err := sqltype.Ltree(id, "ltree", "public.ltree")
	if err != nil { t.Fatal(err) }
	return d
}

func TestLtreeInput(t *testing.T) {
	ctx := context.Background()
	d := ltreeUnitType(t,65000)
	for _, tc := range []struct{ id sqltype.ID; name, declaration string }{
		{0,"ltree","public.ltree"}, {sqltype.Unknown,"ltree","public.ltree"}, {65000,"","public.ltree"}, {65000,"ltree",""},
	} {
		if typ,err:=sqltype.Ltree(tc.id,tc.name,tc.declaration); err==nil || typ!=nil { t.Fatal("invalid descriptor",tc) }
	}
	for _, text := range []string{"", "Top.Countries.Europe", "42._.-.a-b_c", "AbC.aBc", "_", "-", "0", "NULL", strings.Repeat("x",999),strings.Repeat("x",1000),strings.Repeat("a.",65533)+"a",strings.Repeat("a.",65534)+"a"} {
		v,err:=d.Input(ctx,text,-1)
		if err!=nil || v.Type()!=d || v.IsNull() || !v.ValidResult(d.Usage()) { t.Fatal("input",len(text),err) }
		out,err:=d.Output(ctx,v)
		if err!=nil || out!=text { t.Fatal("exact output",len(text),err) }
		again,err:=d.Input(ctx,text,-1)
		if err!=nil || !sqltype.SameDatum(v,again) { t.Fatal("datum",err) }
		if same,err:=v.SamePayload(ctx,again); err!=nil || !same { t.Fatal("payload",same,err) }
	}
	for _,tc:=range []struct{input,code,message,detail string}{
		{"a.", "42601", "ltree syntax error", "Unexpected end of input."},
		{".a", "42601", "ltree syntax error at character 1", ""},
		{".", "42601", "ltree syntax error at character 1", ""},
		{"a..b", "42601", "ltree syntax error at character 3", ""},
		{"a b", "42601", "ltree syntax error at character 2", ""},
		{"a*", "42601", "ltree syntax error at character 2", ""},
		{"a/", "42601", "ltree syntax error at character 2", ""},
		{"a\t", "42601", "ltree syntax error at character 2", ""},
		{"a\n", "42601", "ltree syntax error at character 2", ""},
		{" ", "42601", "ltree syntax error at character 1", ""},
		{"\t", "42601", "ltree syntax error at character 1", ""},
		{"\n", "42601", "ltree syntax error at character 1", ""},
		{"é", "42601", "ltree syntax error at character 1", ""},
		{"雪", "42601", "ltree syntax error at character 1", ""},
		{"١", "42601", "ltree syntax error at character 1", ""},
		{"１", "42601", "ltree syntax error at character 1", ""},
		{"́", "42601", "ltree syntax error at character 1", ""},
		{"a.é", "42601", "ltree syntax error at character 3", ""},
		{"á", "42601", "ltree syntax error at character 2", ""},
		{strings.Repeat("x",1001),"42622","label string is too long","Label length is 1001, must be at most 1000, at character 1002."},
		{"a."+strings.Repeat("x",1001),"42622","label string is too long","Label length is 1001, must be at most 1000, at character 1004."},
		{strings.Repeat("x",1001)+".a","42622","label string is too long","Label length is 1001, must be at most 1000, at character 1002."},
		{strings.Repeat("x",1001)+"/","42601","ltree syntax error at character 1002",""},
		{strings.Repeat("a.",65535)+"a","54000","number of ltree labels (65536) exceeds the maximum allowed (65535)",""},
		{strings.Repeat(".",65535),"54000","number of ltree labels (65536) exceeds the maximum allowed (65535)",""},
		{"/"+strings.Repeat(".",65535),"54000","number of ltree labels (65536) exceeds the maximum allowed (65535)",""},
		{"a\x00","22021",`invalid byte sequence for encoding "UTF8": 0x00`,""},
		{"a\xff","22021",`invalid byte sequence for encoding "UTF8": 0xff`,""},
	} {
		v,err:=d.Input(ctx,tc.input,-1)
		var diagnostic *sqlstate.Error
		if v.Type()!=nil || !errors.As(err,&diagnostic) { t.Fatal("invalid input returned datum",len(tc.input),err) }
		want:=&sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:tc.code,Message:tc.message,Detail:tc.detail}
		if !reflect.DeepEqual(diagnostic,want) { t.Fatalf("diagnostic length %d: %#v want %#v",len(tc.input),diagnostic,want) }
	}
}

func TestLtreeOutput(t *testing.T) {
	d:=ltreeUnitType(t,65000)
	for _,text:=range []string{"", "Top.Countries.Europe", "NULL"} {
		v,err:=d.Input(t.Context(),text,-1); if err!=nil { t.Fatal(err) }
		literal,err:=d.SQLLiteral(t.Context(),v)
		if err!=nil || literal!="'"+text+"'" { t.Fatal("literal",literal,err) }
	}
}

func TestLtreeIdentity(t *testing.T) {
	ctx := context.Background()
	d, foreign, destination := ltreeUnitType(t,65000), ltreeUnitType(t,65000), ltreeUnitType(t,65001)
	if !d.IsLtree() || !d.IsUserDefined() || d.IsBuiltin() || !d.Concrete() || d.DatabaseName() != "LTREE" { t.Fatal("scalar identity") }
	facts, ok := d.PGTypeMetadata()
	if !ok || facts.Length != -1 || facts.ByValue || facts.Alignment != "i" || facts.Storage != "x" || facts.Kind != "b" || facts.Category != "U" || facts.Modifier != -1 || facts.Collation != 0 { t.Fatal("catalog facts",facts,ok) }
	v, err := d.Input(ctx,"",-1)
	if err != nil { t.Fatal(err) }
	for _, value := range []sqltype.Value{v,sqltype.Null(d)} {
		if same, err := value.Retag(d); err != nil || same != value { t.Fatal("same generation",err) }
		for _, target := range []*sqltype.Descriptor{foreign,destination} {
			if _, err := value.Retag(target); err == nil { t.Fatal("foreign retag admitted") }
		}
		mapped, err := value.RemapLtree(destination)
		if err != nil || mapped.Type() != destination || mapped.IsNull() != value.IsNull() { t.Fatal("authorized remap",err) }
		back, err := mapped.RemapLtree(d)
		if err != nil || back != value { t.Fatal("remap copied immutable payload",err) }
	}
	var invalid sqltype.Value
	if invalid.ValidResult(d.Usage()) { t.Fatal("missing payload admitted") }
	if _, err := invalid.Retag(d); err == nil { t.Fatal("invalid datum retagged") }
	if _, err := invalid.RemapLtree(d); err == nil { t.Fatal("invalid datum imported") }
	if _, err := d.Output(ctx,invalid); err == nil { t.Fatal("invalid payload output") }
	other, _ := d.Input(ctx,"bar",-1)
	if sqltype.SameDatum(v,other) { t.Fatal("distinct canonical bytes compare equal") }
	if same, err := v.SamePayload(ctx,other); err != nil || same { t.Fatal("distinct payload",same,err) }
	if equal, order := sqltype.ComparisonCapabilities(d); !equal || !order { t.Fatal("missing typed comparison capability") }
	array, err := sqltype.Array(65002,"_ltree",d)
	if err != nil || !array.Concrete() || array.Element() != d { t.Fatal("concrete exact companion",err) }
	value, err := array.Input(ctx,`{"",NULL,a.b}`,-1)
	if err != nil || value.ArrayLen() != 3 || value.ArrayElement(0).IsNull() || !value.ArrayElement(1).IsNull() { t.Fatal("array codec",err) }
	for i := range value.ArrayLen() {
		if value.ArrayElement(i).Type() != d { t.Fatal("array lost scalar generation") }
	}
	if _, err := sqltype.ArrayValue(array,[]sqltype.ArrayDimension{{Lower:1,Length:1}},[]sqltype.Value{sqltype.Null(foreign)}); err == nil { t.Fatal("foreign null array element admitted") }
}

func TestLtreeCoercion(t *testing.T) {
	d:=ltreeUnitType(t,65000)
	builtins:=map[sqltype.ID]*sqltype.Descriptor{}
	for _,typ:=range sqltype.Builtins() { builtins[typ.ID()]=typ }
	for _,text:=range []string{"", "a.b"} {
		for _,source:=range []*sqltype.Descriptor{builtins[sqltype.Text],builtins[sqltype.VarChar]} {
			value,err:=source.Input(t.Context(),text,-1); if err!=nil { t.Fatal(err) }
			p,err:=sqltype.PlanCoercion(source.Usage(),d.Usage(),sqltype.Explicit)
			if err!=nil { t.Fatal("explicit input plan",err) }
			v,err:=p.Apply(t.Context(),value,sqltype.InputEnvironment{},sqltype.OutputEnvironment{})
			if err!=nil || v.Type()!=d || v.IsNull() { t.Fatal("explicit input",err) }
			out,err:=d.Output(t.Context(),v)
			if err!=nil || out!=text { t.Fatal("coerced output",out,err) }
			for _,target:=range []*sqltype.Descriptor{builtins[sqltype.Text],builtins[sqltype.Int4]} {
				if _,err:=v.RemapLtree(target); err==nil { t.Fatal("foreign scalar remapped") }
			}
		}
	}
	for _,target:=range []*sqltype.Descriptor{builtins[sqltype.Int4],builtins[sqltype.Bool]} {
		if _,err:=sqltype.PlanCoercion(d.Usage(),target.Usage(),sqltype.Explicit); err==nil { t.Fatal("unrelated cast granted") }
	}
}

type ltreeCancelContext struct { context.Context; cancel context.CancelFunc; calls int; at int }
func (c *ltreeCancelContext) Err() error {
	c.calls++
	if c.calls == c.at { c.cancel() }
	return c.Context.Err()
}

func TestLtreeCancellation(t *testing.T) {
	d := ltreeUnitType(t,65000)
	v, err := d.Input(context.Background(),"",-1)
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithCancel(context.Background()); cancel()
	if _, err := d.Input(ctx,"a",-1); !errors.Is(err,context.Canceled) { t.Fatal("input cancellation",err) }
	if _, err := d.Output(ctx,v); !errors.Is(err,context.Canceled) { t.Fatal("output cancellation",err) }
	if _, err := d.SQLLiteral(ctx,v); !errors.Is(err,context.Canceled) { t.Fatal("literal cancellation",err) }
	for _, tc := range []struct { text string; at int }{
		{strings.Repeat("a.",4096)+"a",3},
		{strings.Repeat("a.",4096)+"a",40},
	} {
		base, stop := context.WithCancel(context.Background())
		periodic := &ltreeCancelContext{Context:base,cancel:stop,at:tc.at}
		value, err := d.Input(periodic,tc.text,-1)
		stop()
		if !errors.Is(err,context.Canceled) || value.Type() != nil || periodic.calls != tc.at { t.Fatal("periodic cancellation",periodic.calls,err) }
	}
}

