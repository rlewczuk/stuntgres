package sqltype_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestStatisticsMCVNativeStructured(t *testing.T) {
	types := mcvNativeTypes()
	for _, tc := range []struct{ text, hex string }{
		{`null`, "0100005000000040"},
		{`true`, "0100005000000030"},
		{`false`, "0100005000000020"},
		{`"ab"`, "01000050020000006162"},
		{`1`, "01000050080000102000000000800100"},
		{`[]`, "00000040"}, {`{}`, "00000020"},
		{`[true,null,"x"]`, "0300004000000030000000400100000078"},
		{`{"bb":[true,null],"a":1}`, "020000200100000002000000090000100c000050616262002000000000800100020000400000003000000040"},
	} {
		t.Run(tc.text, func(t *testing.T) { mcvNativeWant(t, mcvNativeInput(t, types[sqltype.JSONB], tc.text), tc.hex) })
	}
	cube, err := sqltype.Cube(90001, "cube", "public.cube")
	if err != nil {
		t.Fatal(err)
	}
	mcvNativeWant(t, mcvNativeInput(t, cube, "(1,2)"), "02000080000000000000f03f0000000000000040")
	mcvNativeWant(t, mcvNativeInput(t, cube, "(1),(3)"), "01000000000000000000f03f0000000000000840")
	nanCube := mcvNativeImage(t, mcvNativeInput(t, cube, "(NaN)"))
	if len(nanCube) != 12 || binary.LittleEndian.Uint32(nanCube) != 0x80000001 || !math.IsNaN(math.Float64frombits(binary.LittleEndian.Uint64(nanCube[4:]))) {
		t.Fatalf("point NaN layout %x", nanCube)
	}
	hstore, err := sqltype.Hstore(90002, "hstore", "public.hstore")
	if err != nil {
		t.Fatal(err)
	}
	mcvNativeWant(t, mcvNativeInput(t, hstore, ""), "00000080")
	mcvNativeWant(t, mcvNativeInput(t, hstore, `"a"=>NULL,"bb"=>""`), "0200008001000080010000400300000003000000616262")
	mcvNativeWant(t, mcvNativeInput(t, hstore, `"a"=>"x"`), "0100008001000080020000006178")
	enum, err := sqltype.EnumWithMembers(90003, "enum", "public.enum", []sqltype.EnumMember{{ID: 101, Label: "z", SortOrder: 1}, {ID: 105, Label: "a", SortOrder: 2}})
	if err != nil {
		t.Fatal(err)
	}
	mcvNativeWant(t, mcvNativeInput(t, enum, "a"), "69000000")
	domain, err := sqltype.Domain(90004, "dom", "public.dom", enum.Usage())
	if err != nil {
		t.Fatal(err)
	}
	mcvNativeWant(t, mcvNativeInput(t, domain, "z"), "65000000")
	// The logical dictionary OID is a domain, but the payload remains its base.
	integerDomain, err := sqltype.Domain(90005, "intdom", "public.intdom", types[sqltype.Int4].Usage())
	if err != nil {
		t.Fatal(err)
	}
	mcvNativeWant(t, mcvNativeInput(t, integerDomain, "-7"), "f9ffffff")
	// Explicit stack traversal survives deep immutable input and cancels within it.
	deep := mcvNativeInput(t, types[sqltype.JSONB], strings.Repeat("[", 256)+"1"+strings.Repeat("]", 256))
	payload := mcvNativeImage(t, deep)
	if len(payload) != 256*8+8 {
		t.Fatalf("nested extent %d", len(payload))
	}
	var dict [8][]sqltype.Value
	dict[0] = []sqltype.Value{deep}
	for _, limit := range []int{10, 200, 1000} {
		ctx := &mcvNativeCancel{Context: context.Background(), remaining: limit}
		if v, err := sqltype.StatisticsMCVBuild(ctx, types[sqltype.PgMCVList], []sqltype.Usage{deep.Type().Usage()}, dict, []sqltype.StatisticsMCVItem{{Frequency: 1, BaseFrequency: 1}}); !errors.Is(err, context.Canceled) || v.Type() != nil {
			t.Fatal("nested cancellation", limit, err)
		}
	}
}

func TestStatisticsMCVNativeArrays(t *testing.T) {
	types := mcvNativeTypes()
	for _, tc := range []struct {
		id        sqltype.ID
		text, hex string
	}{
		{1007, `{}`, "000000000000000017000000"},
		{1007, `[-1:0]={1,2}`, "01000000000000001700000002000000ffffffff0100000002000000"},
		{1007, `[2:3][4:5]={{1,2},{3,4}}`, "0200000000000000170000000200000002000000020000000400000001000000020000000300000004000000"},
		{1009, `[-2:0]={a,NULL,bb}`, "01000000200000001900000003000000feffffff05000000000000001400000061000000180000006262"},
		{1007, `{NULL,NULL}`, "01000000200000001700000002000000010000000000000000000000"},
		{sqltype.Int2Vector, `1 2`, "010000000000000015000000020000000000000001000200"},
		{sqltype.Int2Vector, ` `, "0100000000000000150000000000000000000000"},
		{sqltype.OidVector, `1 2`, "01000000000000001a00000002000000000000000100000002000000"},
	} {
		t.Run(tc.text+"/"+types[tc.id].Name(), func(t *testing.T) { mcvNativeWant(t, mcvNativeInput(t, types[tc.id], tc.text), tc.hex) })
	}
	// Domains over enum elements retain the current domain OID in ArrayType and
	// current pg_enum member OIDs in native element words, never label strings.
	enum, err := sqltype.EnumWithMembers(91001, "e", "public.e", []sqltype.EnumMember{{ID: 117, Label: "z", SortOrder: 1}})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sqltype.Domain(91002, "d", "public.d", enum.Usage())
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(91003, "_d", domain)
	if err != nil {
		t.Fatal(err)
	}
	value := mcvNativeInput(t, array, `{z,NULL}`)
	got := mcvNativeImage(t, value)
	want := make([]byte, 32)
	binary.LittleEndian.PutUint32(want, 1)
	binary.LittleEndian.PutUint32(want[4:], 32)
	binary.LittleEndian.PutUint32(want[8:], 91002)
	binary.LittleEndian.PutUint32(want[12:], 2)
	binary.LittleEndian.PutUint32(want[16:], 1)
	want[20] = 1
	binary.LittleEndian.PutUint32(want[28:], 117)
	if !bytes.Equal(got, want) {
		t.Fatalf("domain enum array %x want %x", got, want)
	}
	// Same labels under a genuinely different destination generation remint both
	// element and member IDs rather than adopting the source byte representation.
	destEnum, err := sqltype.EnumWithMembers(92001, "e", "public.e", []sqltype.EnumMember{{ID: 317, Label: "z", SortOrder: 1}})
	if err != nil {
		t.Fatal(err)
	}
	destDomain, err := sqltype.Domain(92002, "d", "public.d", destEnum.Usage())
	if err != nil {
		t.Fatal(err)
	}
	destArray, err := sqltype.Array(92003, "_d", destDomain)
	if err != nil {
		t.Fatal(err)
	}
	dest := mcvNativeImage(t, mcvNativeInput(t, destArray, `{z,NULL}`))
	if binary.LittleEndian.Uint32(dest[8:]) != 92002 || binary.LittleEndian.Uint32(dest[28:]) != 317 {
		t.Fatalf("destination identities %x", dest)
	}
	var dict [8][]sqltype.Value
	dict[0] = []sqltype.Value{value}
	ctx := &mcvNativeCancel{Context: context.Background(), remaining: 8}
	if _, err := sqltype.StatisticsMCVBuild(ctx, types[sqltype.PgMCVList], []sqltype.Usage{array.Usage()}, dict, []sqltype.StatisticsMCVItem{{Frequency: 1, BaseFrequency: 1}}); !errors.Is(err, context.Canceled) {
		t.Fatal("array cancellation", err)
	}
}

func TestStatisticsMCVNativeGrouping(t *testing.T) {
	types := mcvNativeTypes()
	for _, tc := range []struct {
		id      sqltype.ID
		a, b, c string
	}{
		{sqltype.Inet, "192.0.2.1/24", "192.0.2.1/24", "192.0.2.1/32"},
		{sqltype.Cidr, "2001:db8::/64", "2001:db8:0:0::/64", "2001:db8::/65"},
		{sqltype.PgLSN, "FFFFFFFF/FFFFFFFF", "ffffffff/ffffffff", "0/1"},
	} {
		g, err := sqltype.BindGrouping(types[tc.id].Usage())
		if err != nil {
			t.Fatal(err)
		}
		a := mcvNativeInput(t, types[tc.id], tc.a)
		b := mcvNativeInput(t, types[tc.id], tc.b)
		c := mcvNativeInput(t, types[tc.id], tc.c)
		equal, err := g.Equal(t.Context(), a, b)
		if err != nil || !equal {
			t.Fatal("equivalent grouping", err)
		}
		ha, err := g.Hash(t.Context(), a)
		if err != nil {
			t.Fatal(err)
		}
		hb, err := g.Hash(t.Context(), b)
		if err != nil || ha != hb {
			t.Fatal("inconsistent grouping hash", err)
		}
		equal, err = g.Equal(t.Context(), a, c)
		if err != nil || equal {
			t.Fatal("distinct grouping", err)
		}
		equal, err = g.Equal(t.Context(), sqltype.Null(types[tc.id]), sqltype.Null(types[tc.id]))
		if err != nil || !equal {
			t.Fatal("NULL grouping", err)
		}
	}
	for _, id := range []sqltype.ID{sqltype.JSON, sqltype.Bit, sqltype.TSVector, sqltype.Int4Range} {
		if _, err := sqltype.BindGrouping(types[id].Usage()); err == nil {
			t.Fatal("rejected family acquired grouping", id)
		}
	}
}
