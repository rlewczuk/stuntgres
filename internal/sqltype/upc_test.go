package sqltype

import (
	"context"
	"testing"
)

func TestUpcCodec(t *testing.T) {
	d, err := Upc(60000, "upc", "public.upc")
	if err != nil { t.Fatal(err) }
	ctx := context.Background()
	for _, tc := range []struct{ input string; packed int64; output, literal string }{
		{"123456789012", 246913578024, "123456789012", "'0123456789012'"},
		{"0123456789012", 246913578024, "123456789012", "'0123456789012'"},
		{"0-123-45678901-2", 246913578024, "123456789012", "'0123456789012'"},
		{"123456789010!", 246913578025, "123456789012!", "'0123456789012!'"},
		{"123456789012!", 246913578025, "123456789012!", "'0123456789012!'"},
		{"12345678901?!", 246913578024, "123456789012", "'0123456789012'"},
		{"000000000000", 0, "000000000000", "'0000000000000'"},
		{"000000000000!", 1, "000000000000!", "'0000000000000!'"},
		{"00000000001?", 34, "000000000017", "'0000000000017'"},
		{"999999999993", 1999999999986, "999999999993", "'0999999999993'"},
		{"999999999993!", 1999999999987, "999999999993!", "'0999999999993!'"},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil || v.integer != tc.packed || !v.ValidResult(d.Usage()) { t.Fatal(tc.input, v, err) }
		if got, err := d.Output(ctx, v); err != nil || got != tc.output { t.Fatal(tc.input, got, err) }
		if got, err := d.SQLLiteral(ctx, v); err != nil || got != tc.literal { t.Fatal(tc.input, got, err) }
		for _, text := range []string{tc.output, tc.literal[1:len(tc.literal)-1]} {
			back, err := d.Input(ctx, text, -1)
			if err != nil || !SameDatum(v, back) { t.Fatal("lossy roundtrip", text, err) }
		}
	}
	for _, text := range []string{"", "bad", "12345678901", "123456789010", "0123456789010", "9780123456786", "9790123456785", "9771234567898", "1234567890128", "123456789X", "M123456785", "12345679", "12345678901234", "123456789012 ", "123456789012-", "123\t456789012", "123\n456789012", "!123456789012", "123456789012!!", "123456?89012", "12345678901X"} {
		if _, err := d.Input(ctx, text, -1); err == nil { t.Fatal("invalid input admitted", text) }
	}
	cancelled, cancel := context.WithCancel(ctx); cancel()
	if _, err := d.Input(cancelled, "123456789012", -1); err != context.Canceled { t.Fatal(err) }
	facts, ok := d.PGTypeMetadata()
	if !ok || facts.Kind != "b" || facts.Category != "U" || facts.Length != 8 || !facts.ByValue || facts.Alignment != "d" || facts.Storage != "p" || facts.Modifier != -1 || facts.Collation != 0 { t.Fatal(facts, ok) }
}

func TestUpcIdentity(t *testing.T) {
	d, _ := Upc(60000, "upc", "public.upc")
	foreign, _ := Upc(d.ID(), "renamed", "other.renamed")
	v, _ := d.Input(context.Background(), "123456789012", -1)
	marked, _ := d.Input(context.Background(), "123456789010!", -1)
	if !d.IsUpc() || !d.IsUserDefined() || d.IsEan13() || d.IsIssn13() || d.DatabaseName() != "UPC" || SameDatum(v, marked) || !SameDatum(v, v) { t.Fatal("distinct role and flag") }
	for _, source := range []Value{v, marked, Null(d)} {
		if _, err := source.Retag(foreign); err == nil { t.Fatal("foreign generation retagged") }
		mapped, err := source.RemapUpc(foreign)
		if err != nil || mapped.Type() != foreign || mapped.integer != source.integer || mapped.IsNull() != source.IsNull() || SameDatum(source, mapped) { t.Fatal(mapped, err) }
		for _, construct := range []func(ID,string,string)(*Descriptor,error){Ean13,Isbn,Isbn13,Ismn,Ismn13,Issn,Issn13} {
			sibling, _ := construct(d.ID(), "upc", "public.upc")
			if _, err := source.Retag(sibling); err == nil { t.Fatal("sibling retagged") }
			if _, err := source.RemapUpc(sibling); err == nil { t.Fatal("sibling imported") }
			if source.ValidResult(sibling.Usage()) { t.Fatal("sibling result") }
		}
	}
	for _, packed := range []int64{-1, 2, 246913578020, 2000000000000, 19560246913572} {
		bad := Value{typ:d,integer:packed}
		if bad.ValidResult(d.Usage()) || SameDatum(bad,bad) { t.Fatal("invalid payload",packed) }
		if _,err:=d.Output(context.Background(),bad);err==nil{t.Fatal("invalid output")}
		if _,err:=d.SQLLiteral(context.Background(),bad);err==nil{t.Fatal("invalid serialization")}
		if _,err:=bad.RemapUpc(foreign);err==nil{t.Fatal("invalid import")}
	}
}

func TestUpcCapabilities(t *testing.T) {
	for _,tc:=range []struct{id ID;name,sql string}{{0,"upc","upc"},{Unknown,"upc","upc"},{60000,"","upc"},{60000,"upc",""}} {
		if _,err:=Upc(tc.id,tc.name,tc.sql);err==nil{t.Fatal("invalid descriptor")}
	}
	d,_:=Upc(60000,"upc","public.upc")
	array:=LookupArray(60001,"_upc",d)
	if array.Concrete() || array.Element()!=d {t.Fatal("lookup-only companion")}
	if _,err:=array.Input(context.Background(),"{123456789012}",-1);err==nil{t.Fatal("array input")}
	if d.CheckBinaryInput()==nil || d.CheckBinaryOutput()==nil{t.Fatal("binary provider")}
	if equality,ordering:=ComparisonCapabilities(d);equality||ordering{t.Fatal("comparison provider")}
}

func TestUpcCoercion(t *testing.T) {
	ctx:=context.Background()
	u,_:=Upc(60000,"upc","public.upc"); e,_:=Ean13(60001,"ean13","public.ean13")
	for _,tc:=range []struct{from,to *Descriptor; mode CoercionContext; allowed bool}{{u,e,Implicit,false},{u,e,Assignment,true},{u,e,Explicit,true},{e,u,Implicit,false},{e,u,Assignment,false},{e,u,Explicit,true}} {
		p,err:=PlanCoercion(tc.from.Usage(),tc.to.Usage(),tc.mode)
		if (err==nil)!=tc.allowed{t.Fatal(tc,err)}
		if !tc.allowed{continue}
		// The released reverse cast is WITHOUT FUNCTION AS ASSIGNMENT;
		// only the checked EAN13-to-UPC direction evaluates a cast function.
		wantFunction, wantCount := tc.from == e, 0
		if wantFunction { wantCount = 1 }
		if p.FunctionExpression() != wantFunction || p.EvaluationFunctionCount() != wantCount {
			t.Fatalf("%s to %s classification = %v/%d, want %v/%d", tc.from.DatabaseName(), tc.to.DatabaseName(), p.FunctionExpression(), p.EvaluationFunctionCount(), wantFunction, wantCount)
		}
		for _,input:=range []string{"0123456789012","0123456789010!"} {
			v,err:=tc.from.Input(ctx,input,-1);if err!=nil{t.Fatal(err)}
			got,err:=p.Apply(ctx,v,InputEnvironment{},OutputEnvironment{});if err!=nil||got.Type()!=tc.to||got.integer!=v.integer{t.Fatal(got,err)}
		}
		got,err:=p.Apply(ctx,Null(tc.from),InputEnvironment{},OutputEnvironment{});if err!=nil||!got.IsNull()||got.Type()!=tc.to{t.Fatal(got,err)}
		foreign,_:=Upc(tc.from.ID(),"foreign","foreign")
		if _,err:=p.Apply(ctx,Null(foreign),InputEnvironment{},OutputEnvironment{});err==nil{t.Fatal("foreign source NULL")}
		cancelled,cancel:=context.WithCancel(ctx);cancel()
		if _,err:=p.Apply(cancelled,Null(tc.from),InputEnvironment{},OutputEnvironment{});err!=context.Canceled{t.Fatal(err)}
	}
	p,_:=PlanCoercion(e.Usage(),u.Usage(),Explicit)
	for _,text:=range []string{"9780123456786","1234567890128"}{v,err:=e.Input(ctx,text,-1);if err!=nil{t.Fatal(err)};if _,err:=p.Apply(ctx,v,InputEnvironment{},OutputEnvironment{});err==nil{t.Fatal("wrong subset",text)}}
	for _,construct:=range []func(ID,string,string)(*Descriptor,error){Isbn,Isbn13,Ismn,Ismn13,Issn,Issn13}{
		sibling,_:=construct(60002,"sibling","sibling")
		for _,pair:=range [][2]*Descriptor{{u,sibling},{sibling,u}}{if _,err:=PlanCoercion(pair[0].Usage(),pair[1].Usage(),Explicit);err==nil{t.Fatal("unrelated cast")}}
	}
}
