package fts

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestFTSStatAccumulator(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		weights string
		want    Statistics
	}{
		{"", Statistics{"cat": {2, 3}, "dog": {2, 3}, "solo": {1, 1}}},
		{"xyz", Statistics{"cat": {2, 3}, "dog": {2, 3}, "solo": {1, 1}}},
		{"AaBb", Statistics{"cat": {1, 2}, "dog": {1, 1}}},
		{"d", Statistics{"dog": {2, 2}}}, {"c", Statistics{"cat": {1, 1}}},
	} {
		counts := make(Statistics)
		for _, text := range []string{"cat:1A,2B dog:3D solo", "cat:4C dog:1A,2D", ""} {
			v, err := ParseVector(ctx, text)
			if err != nil {
				t.Fatal(err)
			}
			before := v.String()
			entries := slices.Clone(v.lexemes)
			for i := range entries {
				entries[i].positions = slices.Clone(entries[i].positions)
			}
			if err := v.AccumulateStatistics(ctx, StatisticsWeights(tc.weights), counts); err != nil {
				t.Fatal(err)
			}
			if v.String() != before || !reflect.DeepEqual(entries, v.lexemes) {
				t.Fatal("statistics mutated immutable vector")
			}
		}
		if !reflect.DeepEqual(counts, tc.want) {
			t.Fatal(tc.weights, counts, tc.want)
		}
	}
	for _, tc := range []struct {
		text, weights string
		want          Statistics
	}{
		{"x:1A,1B,2D", "", Statistics{"x": {1, 2}}}, {"x:1A,1B,2D", "A", Statistics{"x": {1, 1}}},
		{"x:16383A,16384B,999999D", "", Statistics{"x": {1, 1}}},
		{"x", "", Statistics{"x": {1, 1}}}, {"x", "abcd", Statistics{}},
	} {
		v, err := ParseVector(ctx, tc.text)
		if err != nil {
			t.Fatal(err)
		}
		counts := make(Statistics)
		if err := v.AccumulateStatistics(ctx, StatisticsWeights(tc.weights), counts); err != nil || !reflect.DeepEqual(counts, tc.want) {
			t.Fatal(tc, counts, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	v, err := ParseVector(ctx, "x:1")
	if err != nil {
		t.Fatal(err)
	}
	counts := make(Statistics)
	if err := v.AccumulateStatistics(canceled, 0, counts); !errors.Is(err, context.Canceled) || len(counts) != 0 {
		t.Fatal("canceled publication", counts, err)
	}
}
