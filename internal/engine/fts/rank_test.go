package fts

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
)

func rankOperands(t *testing.T, vector, query string) (*Vector, *Query) {
	t.Helper()
	v, err := ParseVector(t.Context(), vector)
	if err != nil {
		t.Fatal(err)
	}
	q, err := ParseQuery(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	return v, q
}
func rankUnit(t *testing.T, vector, query string, flags int32, cd bool, want float32, tolerance float64) {
	t.Helper()
	v, q := rankOperands(t, vector, query)
	got, err := v.Rank(t.Context(), q, [4]float32{.1, .2, .4, 1}, flags, cd)
	if err != nil || math.IsNaN(float64(got)) || math.Abs(float64(got-want)) > tolerance {
		t.Fatalf("Rank(%q,%q,%d,%t)=%g,%v want %g", vector, query, flags, cd, got, err, want)
	}
}
func TestFTSRankFrequency(t *testing.T) {
	for _, tc := range []struct {
		vector, query string
		value         float32
	}{
		{"cat:1", "cat", .0607927106320858}, {"cat:1,3", "cat", .07599088549613953},
		{"cat", "cat", .0607927106320858}, {"", "cat", 0}, {"cat:1", "", 0},
		{"cat:1", "cat|rat", .0303963553160429}, {"cat:1", "!cat", .0607927106320858},
		{"cat:1", "cat:A", .0607927106320858}, {"cat:1 cats:2", "cat|cat:*", .1215854212641716},
	} {
		rankUnit(t, tc.vector, tc.query, 0, false, tc.value, 1e-8)
	}
}
func TestFTSRankPairs(t *testing.T) {
	for _, tc := range []struct {
		vector, query string
		value         float32
	}{
		{"cat:1 rat:2", "cat & rat", .09910321980714798},
		{"cat:1 rat:3", "cat <-> rat", .09850085526704788},
		{"cat:1", "cat & rat", 1e-20}, {"cat:1 rat:1", "cat & rat", 1e-20},
		{"cat rat", "cat & rat", 1e-16}, {"cat:1 cats:2", "cat:* & cats", 1e-20},
		{"aa:1 ab:10 b:11", "a:* & b", .09910321980714798},
		{"cat:1 rat:102", "cat & rat", 1e-16},
	} {
		rankUnit(t, tc.vector, tc.query, 0, false, tc.value, math.Abs(float64(tc.value))*2e-7)
	}
}
func TestFTSRankNormalization(t *testing.T) {
	for _, tc := range []struct {
		flag     int32
		rank, cd float32
	}{
		{0, .07599088549613953, .20000000298023224}, {1, .029397286474704742, .11162212491035461},
		{2, .015198176726698875, .03999999910593033}, {4, .07599088549613953, .05000000074505806},
		{8, .01899772137403488, .05000000074505806}, {16, .03272749483585358, .08613531291484833},
		{32, .07062409818172455, .1666666716337204}, {63, .0006326356087811291, .0006005520117469132},
		{64, .07599088549613953, .20000000298023224}, {-1, .0006326356087811291, .0006005520117469132},
	} {
		rankUnit(t, "cat:1,3 rat:10000 stripped zebra", "cat", tc.flag, false, tc.rank, 1e-8)
		rankUnit(t, "cat:1,3 rat:10000 stripped zebra", "cat", tc.flag, true, tc.cd, 1e-8)
	}
}
func TestFTSRankBinary32(t *testing.T) {
	for _, tc := range []struct {
		vector string
		bits   uint32
	}{{"cat:1D,2A", 0x3f2b3139}, {"cat:1A,2D", 0x3f1f8524}, {"cat:1A,2D,3A", 0x3f30cfef}, {"cat:1,3", 0x3d9ba11c}} {
		v, q := rankOperands(t, tc.vector, "cat")
		got, err := v.Rank(t.Context(), q, [4]float32{.1, .2, .4, 1}, 0, false)
		if err != nil || math.Float32bits(got) != tc.bits {
			t.Fatalf("%q bits %08x want %08x: %v", tc.vector, math.Float32bits(got), tc.bits, err)
		}
	}
	v, q := rankOperands(t, "cat:1", "cat")
	negativeZero := math.Float32frombits(0x80000000)
	for _, cd := range []bool{false, true} {
		got, err := v.Rank(t.Context(), q, [4]float32{negativeZero, negativeZero, negativeZero, negativeZero}, 0, cd)
		if err != nil || math.Float32bits(got) != 0 {
			t.Fatal("zero sign", cd, got, err)
		}
	}
}
func TestFTSRankCancellation(t *testing.T) {
	v, q := rankOperands(t, "a:1,3,5,7 b:2,4,6,8 c:9,11,13,15", "a & b & c")
	for _, cd := range []bool{false, true} {
		baseline := &countingContext{Context: t.Context(), after: int(^uint(0) >> 1)}
		if _, err := v.Rank(baseline, q, [4]float32{.1, .2, .4, 1}, 63, cd); err != nil {
			t.Fatal(err)
		}
		for after := 1; after <= baseline.calls; after++ {
			ctx := &countingContext{Context: t.Context(), after: after}
			got, err := v.Rank(ctx, q, [4]float32{.1, .2, .4, 1}, 63, cd)
			if !errors.Is(err, context.Canceled) || got != 0 {
				t.Fatalf("cd=%t checkpoint=%d result=%g error=%v", cd, after, got, err)
			}
		}
	}
}
func TestFTSRankImmutability(t *testing.T) {
	v, q := rankOperands(t, "cat:1D,2A rat:3C", "cat:* & rat")
	beforeV, beforeQ := v.text, q.text
	positions := append([]position(nil), v.lexemes[0].positions...)
	nodes := append([]queryNode(nil), q.nodes...)
	for _, cd := range []bool{false, true} {
		if _, err := v.Rank(t.Context(), q, [4]float32{.1, .2, .4, 1}, 63, cd); err != nil {
			t.Fatal(err)
		}
		ctx := &countingContext{Context: t.Context(), after: 4}
		if _, err := v.Rank(ctx, q, [4]float32{.1, .2, .4, 1}, 0, cd); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if v.text != beforeV || q.text != beforeQ || !reflect.DeepEqual(positions, v.lexemes[0].positions) || !reflect.DeepEqual(nodes, q.nodes) {
		t.Fatal("rank mutated immutable source")
	}
}
