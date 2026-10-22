package fts

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestFTSOpsVectorConcat(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ left, right, want string }{
		{"a:1 b:2", "c:1 d:2 b:3", "'a':1 'b':2,5 'c':3 'd':4"},
		{"a:3A b:2a", "ba:1234 a:1B", "'a':3A,4B 'b':2A 'ba':1237"},
		{"a:1 z:10", "a:2B", "'a':1,12B 'z':10"},
		{"a z", "a:2B b:1", "'a':2B 'b':1 'z'"}, {"a:2A", "a b", "'a':2A 'b'"},
		{"a z", "b a", "'a' 'b' 'z'"},
		{"z:16382", "a:1D,2A", "'a':16383 'z':16382"},
		{"a:16383D", "a:1A", "'a':16383"}, {"", "a:1B", "'a':1B"}, {"a:1B", "", "'a':1B"},
	} {
		a, b := opsVector(t, tc.left), opsVector(t, tc.right)
		beforeA, beforeB := opsVector(t, tc.left), opsVector(t, tc.right)
		got, err := a.Concat(ctx, b)
		if err != nil || got.String() != tc.want {
			t.Fatal(tc, got, err)
		}
		same, err := a.Equal(ctx, beforeA)
		if err != nil || !same {
			t.Fatal("left mutation", err)
		}
		same, err = b.Equal(ctx, beforeB)
		if err != nil || !same {
			t.Fatal("right mutation", err)
		}
	}
	positions := make([]string, 256)
	for i := range positions {
		positions[i] = fmt.Sprint(i + 1)
	}
	for _, n := range []int{255, 256} {
		a := opsVector(t, "a:"+strings.Join(positions[:n], ","))
		b := opsVector(t, "a:1B,2A b:1A")
		got, err := a.Concat(ctx, b)
		want := "'a':" + strings.Join(positions, ",")
		if n == 255 {
			want += "B"
		}
		want += fmt.Sprintf(" 'b':%dA", n+1)
		if err != nil || got.String() != want {
			t.Fatal(n, got, err)
		}
	}
	words := make([]string, 511)
	for i := range words {
		words[i] = fmt.Sprintf("%04d", i) + strings.Repeat("x", 2036) + ":1,2,3,4"
	}
	left := opsVector(t, strings.Join(words, " "))
	for _, size := range []int{1048575, 1048576} {
		tail := opsVector(t, strings.Repeat("z", size-511*2050))
		result, err := left.Concat(ctx, tail)
		if size == 1048576 {
			codecError(t, err, "54000", "string is too long for tsvector (1048576 bytes, max 1048575 bytes)")
			if _, marked := err.(*sqlstate.InputError); marked {
				t.Fatal("vector algebra size failure gained input-origin authority", err)
			}
		} else if err != nil || len(result.lexemes) != 512 {
			t.Fatal(result, err)
		}
	}
}
