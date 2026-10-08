package sqltype

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func binaryPositionOracle(haystack, needle string) int {
	for start := 0; start <= len(haystack)-len(needle); start++ {
		matched := true
		for i := range len(needle) {
			if haystack[start+i] != needle[i] {
				matched = false
				break
			}
		}
		if matched {
			return start
		}
	}
	return -1
}

func TestBinarySlicePositionExhaustive(t *testing.T) {
	// Independently enumerate every octet string up to six bytes over a small
	// alphabet, including NUL/high-bit octets, and every needle up to four.
	words := []string{""}
	level := []string{""}
	for range 6 {
		next := make([]string, 0, len(level)*3)
		for _, prefix := range level {
			for _, octet := range []byte{0, 'a', 255} {
				next = append(next, prefix+string([]byte{octet}))
			}
		}
		words = append(words, next...)
		level = next
	}
	for _, hay := range words {
		for _, needle := range words {
			if len(needle) > 4 {
				continue
			}
			got, err := binaryPosition(context.Background(), hay, needle)
			want := binaryPositionOracle(hay, needle)
			if err != nil || got != want {
				t.Fatalf("hay=%x needle=%x got=%d want=%d err=%v", hay, needle, got, want, err)
			}
		}
	}
}

func TestBinarySlicePositionLongNeedles(t *testing.T) {
	for _, tc := range []struct {
		hay, needle string
		want        int
	}{
		{strings.Repeat("a", 1<<20), strings.Repeat("a", 1<<16) + "b", -1},
		{strings.Repeat("a", 1<<20) + "b", strings.Repeat("a", 1<<16) + "b", (1 << 20) - (1 << 16)},
		{strings.Repeat("ab", 1<<18), strings.Repeat("ab", 1<<14), 0},
		{strings.Repeat("x", 4095) + strings.Repeat("ab", 10000) + "z", strings.Repeat("ab", 10000) + "z", 4095},
		{strings.Repeat("x", 8191) + "\x00\xff" + strings.Repeat("a", 20000), "\x00\xff" + strings.Repeat("a", 20000), 8191},
	} {
		got, err := binaryPosition(context.Background(), tc.hay, tc.needle)
		if err != nil || got != tc.want {
			t.Fatal(got, tc.want, err)
		}
		if allocs := testing.AllocsPerRun(10, func() {
			if _, err := binaryPosition(context.Background(), tc.hay, tc.needle); err != nil {
				panic(err)
			}
		}); allocs != 0 {
			t.Fatal("search scratch allocates", allocs)
		}
	}
	// Long factorization must cancel before it can finish; no giant equality or
	// standard-library search can hide an unchecked needle-sized operation.
	preprocess := &mathCheckpointContext{Context: context.Background(), remaining: 2}
	if _, err := binaryPosition(preprocess, strings.Repeat("a", 1<<20), strings.Repeat("a", 1<<16)+"b"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// This budget exceeds all factorization/periodicity work for this needle;
	// cancellation therefore occurs during progress over the repetitive source.
	matching := &mathCheckpointContext{Context: context.Background(), remaining: 20}
	if _, err := binaryPosition(matching, strings.Repeat("a", 1<<20), strings.Repeat("a", 8192)+"b"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func BenchmarkBinarySlicePosition(b *testing.B) {
	for _, size := range []int{1 << 20, 2 << 20} {
		b.Run(fmt.Sprintf("repetitive_%d", size), func(b *testing.B) {
			hay := strings.Repeat("a", size)
			needle := strings.Repeat("a", size/4) + "b"
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for range b.N {
				got, err := binaryPosition(context.Background(), hay, needle)
				if err != nil || got != -1 {
					b.Fatal(got, err)
				}
			}
		})
	}
}
