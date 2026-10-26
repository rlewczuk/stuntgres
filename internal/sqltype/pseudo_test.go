package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestPseudoTypeMetadata(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct{id ID; input,output,receive,send uint32; length int16; byval bool; align,storage string}{
		{2276,2294,2295,0,0,4,true,"i","p"}, {2283,2312,2313,0,0,4,true,"i","p"},
		{2277,2296,2297,2502,2503,-1,false,"d","x"}, {2776,2777,2778,0,0,4,true,"i","p"},
		{3500,3504,3505,0,0,4,true,"i","p"}, {3831,3832,3833,0,0,-1,false,"d","x"}, {4537,4229,4230,0,0,-1,false,"d","x"},
		{5077,5086,5087,0,0,4,true,"i","p"}, {5078,5088,5089,5090,5091,-1,false,"d","x"}, {5079,5092,5093,0,0,4,true,"i","p"},
		{5080,5094,5095,0,0,-1,false,"d","x"}, {4538,4226,4227,0,0,-1,false,"d","x"},
		{2275,2292,2293,2500,2501,-2,false,"c","p"}, {2281,2304,2305,0,0,8,true,"d","p"},
		{2280,2302,2303,0,0,4,true,"i","p"}, {3115,3116,3117,0,0,4,true,"i","p"}, {269,267,268,0,0,4,true,"i","p"},
		{325,326,327,0,0,4,true,"i","p"}, {3310,3311,3312,0,0,4,true,"i","p"},
		{2249,2290,2291,2402,2403,-1,false,"d","x"}, {2279,2300,2301,0,0,4,true,"i","p"}, {3838,3594,3595,0,0,4,true,"i","p"},
		{32,86,87,88,90,8,true,"d","p"}, {Void,2298,2299,3120,3121,4,true,"i","p"}, {Unknown,109,110,2416,2417,-2,false,"c","p"},
	} {
		d := types[tc.id]
		facts,ok := d.PGTypeMetadata()
		category := "P"; if tc.id == Unknown { category = "X" }
		want := PGTypeMetadata{
			PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{tc.length, tc.byval, tc.align, tc.storage},
			Kind: "p", Category: category, Input: tc.input, Output: tc.output,
			Receive: tc.receive, Send: tc.send, Modifier: -1,
		}
		if !ok || !IsPseudo(tc.id) || facts != want {
			t.Fatalf("OID %d metadata: got %#v, want %#v", tc.id, facts, want)
		}
		var diagnostic *sqlstate.Error
		if err := CheckStoredColumn("v", d.Usage()); !errors.As(err, &diagnostic) || diagnostic.Code != "42P16" {
			t.Fatal("pseudo metadata admitted storage", tc.id, err)
		}
		if tc.id != Void && tc.id != 2275 && d.Concrete() { t.Fatalf("metadata granted concrete authority: %d",tc.id) }
	}
	if PolymorphicFamily(2276) != 0 || PolymorphicFamily(2283) != 1 || PolymorphicFamily(5077) != 2 { t.Fatal("independent families") }
	if types[2276].DiagnosticName() != `"any"` { t.Fatal("ANY diagnostic spelling",types[2276].DiagnosticName()) }
	if types[Unknown].DiagnosticName() != "unknown" || DeferredRecord().DiagnosticName() != "record" {
		t.Fatal("pseudo diagnostic identity", types[Unknown].DiagnosticName(), DeferredRecord().DiagnosticName())
	}
}

func TestPseudoTypeValueAdmission(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct{id ID; input,output string}{{2275,"abc","abc"},{Void,"ignored",""}} {
		v,err := types[tc.id].Input(ctx,tc.input,-1)
		if err != nil || v.IsNull() || !v.ValidResult(types[tc.id].Usage()) { t.Fatal(v,err) }
		out,err := types[tc.id].Output(ctx,v); if err != nil || out != tc.output { t.Fatal(out,err) }
	}
	for _, id := range []ID{2277,5078,3831,5080,4537,4538,32} {
		u := types[id].Usage()
		if !u.QueryNullUsage() || !u.Valid() || !Null(u.Type).ValidResult(u) || u.Type.Concrete() { t.Fatal("query NULL authority",id,u) }
		if LookupOnly(id,u.Type.Name(),0).Usage().QueryNullUsage() { t.Fatal("forged NULL authority",id) }
		var diagnostic *sqlstate.Error
		if err := CheckStoredColumn("v",u); !errors.As(err,&diagnostic) || diagnostic.Code != "42P16" { t.Fatal(id,err) }
	}
	for _, id := range []ID{269,325,2280,3115,3310,2279,3838} {
		_,err := types[id].Input(ctx,"x",-1)
		var diagnostic *sqlstate.Error
		if !errors.As(err,&diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "cannot accept a value of type "+types[id].Name() { t.Fatal(id,err) }
	}
	var diagnostic *sqlstate.Error
	if err := CheckStoredColumn("v",types[2276].Usage()); !errors.As(err,&diagnostic) || diagnostic.Message != `column "v" has pseudo-type "any"` { t.Fatal(err) }
}

func TestPseudoQueryNullAuthority(t *testing.T) {
	types := testTypes()
	admitted := map[ID]bool{2277: true, 5078: true, 3831: true, 5080: true, 4537: true, 4538: true, 32: true}
	for id, d := range types {
		if d.Usage().QueryNullUsage() != admitted[id] {
			t.Fatalf("unexpected query NULL inventory membership: %d", id)
		}
	}
	for id := range admitted {
		d := types[id]
		t.Run(d.Name(), func(t *testing.T) {
			u := d.Usage()
			value := Null(d)
			if !u.Valid() || !value.IsNull() || value.Type() != d || !value.ValidResult(u) || d.Concrete() {
				t.Fatal("canonical query NULL lost its bounded authority")
			}
			if (Value{typ: d}).ValidResult(u) {
				t.Fatal("query NULL capability admitted a non-NULL value")
			}
			if _, err := d.Input(t.Context(), "x", -1); err == nil {
				t.Fatal("query NULL capability admitted a non-NULL input codec")
			}
			if err := CheckStoredColumn("v", u); err == nil {
				t.Fatal("query NULL capability admitted storage")
			}
			copied := *d
			for _, foreign := range []*Descriptor{&copied, LookupOnly(id, d.Name(), 0)} {
				bad := foreign.Usage()
				if bad.QueryNullUsage() || bad.Valid() ||
					(Value{typ: foreign, null: true}).ValidResult(bad) ||
					(Value{typ: foreign}).ValidResult(bad) {
					t.Fatal("copied or lookup-only descriptor manufactured NULL authority")
				}
				func() {
					defer func() {
						if recover() == nil {
							t.Error("NULL constructor admitted a non-owning descriptor")
						}
					}()
					Null(foreign)
				}()
			}
			for _, bad := range []Usage{
				{Type: d, Typmod: 0},
				{Type: d, Typmod: -1, Collation: CollationRef{ID: 950}},
			} {
				if bad.QueryNullUsage() || bad.Valid() || value.ValidResult(bad) {
					t.Fatal("modified usage manufactured NULL authority", bad)
				}
			}
		})
	}
}

func TestPseudoRecordResult(t *testing.T) {
	types := testTypes()
	shape,err := RecordShape([]CompositeMember{{Attribute:1,Name:"a",Usage:types[Int4].Usage()},{Attribute:2,Name:"b",Usage:types[Text].Usage()}}); if err != nil { t.Fatal(err) }
	integer,err := Integer(types[Int4],1); if err != nil { t.Fatal(err) }
	value,err := CompositeValue(shape,[]Value{integer,String(types[Text],"x")}); if err != nil { t.Fatal(err) }
	target := DeferredRecord()
	result,err := RecordValue(target,value); if err != nil || result.Type() != target || result.IsNull() { t.Fatal(result,err) }
	text,err := target.Output(context.Background(),result); if err != nil || text != "(1,x)" { t.Fatal(text,err) }
	if null,err := RecordValue(target,Null(shape)); err != nil || !null.IsNull() || null.Type() != target { t.Fatal(null,err) }
	if _,err := RecordValue(target,integer); err == nil { t.Fatal("scalar accepted as record") }
}
