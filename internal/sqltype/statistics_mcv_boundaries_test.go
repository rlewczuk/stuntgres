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

func TestStatisticsMCVNativeBoundaries(t *testing.T) {
	types := mcvNativeTypes()
	// The binary-created REAL representative retains its exact NaN word and -0.
	for _, bits := range []uint32{0x7fa12345, 0x80000000} {
		data := binary.BigEndian.AppendUint32(nil, bits)
		v, err := types[sqltype.Float4].BinaryInputWithEnvironment(t.Context(), data, -1, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		got := mcvNativeImage(t, v)
		if len(got) != 4 || binary.LittleEndian.Uint32(got) != bits {
			t.Fatalf("REAL bits %x want %x", got, bits)
		}
	}
	// MONEY is the signed native 64-bit cents word, independent of locale output.
	money, err := types[sqltype.Money].BinaryInputWithEnvironment(t.Context(), []byte{255, 255, 255, 255, 255, 255, 255, 254}, -1, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	mcvNativeWant(t, money, "feffffffffffffff")
	for _, tc := range []struct{ text, hex string }{
		{"1e252", "3f800100"}, {"1e-256", "0001c0ff0100"},
		{"1e-260", "0401bfff0100"},
		{"-1." + strings.Repeat("0", 64), "404000000100"},
	} {
		mcvNativeWant(t, mcvNativeInput(t, types[sqltype.Numeric], tc.text), tc.hex)
	}
	// Nested varlena numeric elements include their own ordinary physical header.
	mcvNativeWant(t, mcvNativeInput(t, types[1231], "{1,2}"), "0100000000000000a4060000020000000100000020000000008001002000000000800200")
	// No non-NULL dictionary is fabricated for a NULL-only population, but all
	// dimensions still retain their actual physical type metadata.
	usages := make([]sqltype.Usage, 8)
	for i := range usages {
		usages[i] = types[sqltype.Int4].Usage()
	}
	nulls := [8]bool{true, true, true, true, true, true, true, true}
	value, err := sqltype.StatisticsMCVBuild(t.Context(), types[sqltype.PgMCVList], usages, [8][]sqltype.Value{}, []sqltype.StatisticsMCVItem{{Nulls: nulls, Frequency: 1, BaseFrequency: 1}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := value.Type().AppendBinaryWithEnvironment(t.Context(), nil, value, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 246 || !bytes.Equal(raw[206:214], []byte{1, 1, 1, 1, 1, 1, 1, 1}) {
		t.Fatalf("NULL width-eight %x", raw)
	}
	for d := range 8 {
		at := 46 + 20*d
		if binary.LittleEndian.Uint32(raw[14+4*d:]) != 23 || !bytes.Equal(raw[at:at+12], make([]byte, 12)) || binary.LittleEndian.Uint32(raw[at+12:]) != 4 || raw[at+16] != 1 {
			t.Fatal("NULL dictionary metadata", d)
		}
	}
	if math.Float64frombits(binary.LittleEndian.Uint64(raw[214:])) != 1 || math.Float64frombits(binary.LittleEndian.Uint64(raw[222:])) != 1 {
		t.Fatal("NULL frequencies")
	}
	// Released maximum item count is accepted and cancellation discards the whole
	// result while visiting a nontrivial item population.
	var dict [8][]sqltype.Value
	dict[0] = []sqltype.Value{mcvNativeInput(t, types[sqltype.Int4], "1")}
	items := make([]sqltype.StatisticsMCVItem, 10000)
	for i := range items {
		items[i] = sqltype.StatisticsMCVItem{Frequency: 0.0001, BaseFrequency: 0.0001}
	}
	if _, err := sqltype.StatisticsMCVBuild(t.Context(), types[sqltype.PgMCVList], usages[:1], dict, items); err != nil {
		t.Fatal("maximum items", err)
	}
	ctx := &mcvNativeCancel{Context: context.Background(), remaining: 100}
	if v, err := sqltype.StatisticsMCVBuild(ctx, types[sqltype.PgMCVList], usages[:1], dict, items); !errors.Is(err, context.Canceled) || v.Type() != nil {
		t.Fatal("item loop cancellation", err)
	}
	if _, err := sqltype.StatisticsMCVBuild(t.Context(), types[sqltype.PgMCVList], usages[:1], dict, append(items, items[0])); err == nil {
		t.Fatal("oversized item population")
	}
	// Recursive array and string-copy loops poll cancellation, not just entry.
	large := mcvNativeInput(t, types[sqltype.Text], strings.Repeat("x", 32000))
	dict[0] = []sqltype.Value{large}
	ctx = &mcvNativeCancel{Context: context.Background(), remaining: 12}
	if v, err := sqltype.StatisticsMCVBuild(ctx, types[sqltype.PgMCVList], []sqltype.Usage{large.Type().Usage()}, dict, items[:1]); !errors.Is(err, context.Canceled) || v.Type() != nil {
		t.Fatal("string copy cancellation", err)
	}
}
