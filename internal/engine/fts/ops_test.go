package fts

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func opsQuery(t *testing.T, input string) *Query {
	t.Helper()
	q, err := ParseQuery(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return q
}
func opsVector(t *testing.T, input string) *Vector {
	t.Helper()
	v, err := ParseVector(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func opsRoundtrip(t *testing.T, q *Query) {
	t.Helper()
	text, err := q.InputText(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	other := opsQuery(t, text)
	same, err := q.Equal(context.Background(), other)
	if err != nil || !same {
		t.Fatal("lossless association", text, err)
	}
}
func TestFTSOpsQueryAlgebra(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		left, right string
		op          byte
		want        string
	}{
		{"fat | rat", "cat", '&', "( 'fat' | 'rat' ) & 'cat'"},
		{"fat | rat", "cat", '|', "'fat' | 'rat' | 'cat'"},
		{"!a", "", '!', "!!'a'"}, {"a", "a", '&', "'a' & 'a'"},
		{"a", "b & c", '&', "'a' & 'b' & 'c'"},
		{"a:*A", "b:DC", '|', "'a':*A | 'b':CD"},
	} {
		left, right := opsQuery(t, tc.left), opsQuery(t, tc.right)
		beforeLeft, beforeRight := append([]queryNode(nil), left.nodes...), append([]queryNode(nil), right.nodes...)
		var got *Query
		var err error
		switch tc.op {
		case '&':
			got, err = left.And(ctx, right)
		case '|':
			got, err = left.Or(ctx, right)
		case '!':
			got, err = left.Not(ctx)
		}
		if err != nil || got.String() != tc.want {
			t.Fatal(tc, got, err)
		}
		opsRoundtrip(t, got)
		if !reflect.DeepEqual(beforeLeft, left.nodes) || len(beforeRight) != len(right.nodes) || len(beforeRight) > 0 && !reflect.DeepEqual(beforeRight, right.nodes) {
			t.Fatal("source arena mutated")
		}
	}
	empty, a := opsQuery(t, ""), opsQuery(t, "a")
	for _, op := range []byte{'&', '|', '<'} {
		got, err := empty.compose(ctx, a, op, 1)
		if err != nil || got != a {
			t.Fatal("left empty identity", err)
		}
		got, err = a.compose(ctx, empty, op, 1)
		if err != nil || got != a {
			t.Fatal("right empty identity", err)
		}
	}
	if got, err := empty.Not(ctx); err != nil || got != empty {
		t.Fatal("NOT empty", err)
	}
	// Generated trees are not restricted by the input grammar's pending stack.
	q := a
	for range 64 {
		var err error
		q, err = q.Not(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	if q.String() != strings.Repeat("!", 64)+"'a'" {
		t.Fatal("generated NOT depth")
	}
	opsRoundtrip(t, q)
}
func TestFTSOpsPhraseBounds(t *testing.T) {
	ctx := context.Background()
	a, b := opsQuery(t, "a"), opsQuery(t, "b")
	for _, distance := range []int32{0, 1, 16383, 16384} {
		q, err := a.Phrase(ctx, b, distance)
		op := fmt.Sprintf("<%d>", distance)
		if distance == 1 {
			op = "<->"
		}
		if err != nil || q.String() != "'a' "+op+" 'b'" {
			t.Fatal(q, err)
		}
		opsRoundtrip(t, q)
	}
	for _, distance := range []int32{-1, 16385, -2147483648, 2147483647} {
		for _, left := range []*Query{a, opsQuery(t, "")} {
			_, err := left.Phrase(ctx, b, distance)
			codecError(t, err, "22023", "distance in phrase operator must be an integer value between zero and 16384 inclusive")
		}
	}
	right, err := a.Phrase(ctx, b, 2)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := a.Phrase(ctx, right, 1)
	if err != nil || nested.String() != "'a' <-> ( 'a' <2> 'b' )" {
		t.Fatal(nested, err)
	}
	opsRoundtrip(t, nested)
}
func TestFTSOpsContainment(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		want        bool
	}{
		{"a | !b <2> a", "a & b", true}, {"a:*A", "a:D", true}, {"a:*", "abc", false}, {"a", "A", false},
		{"a", "a & a", true}, {"", "", true}, {"a", "", true}, {"", "a", false},
	} {
		a, b := opsQuery(t, tc.left), opsQuery(t, tc.right)
		got, err := a.Contains(context.Background(), b)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}
func TestFTSOpsLimits(t *testing.T) {
	ctx := context.Background()
	a := opsQuery(t, "z")
	for _, offset := range []int{1048575, 1048576} {
		words := make([]string, 513)
		for i := range 512 {
			words[i] = strings.Repeat("x", 2046)
		}
		words[512] = strings.Repeat("y", offset-512*2047-1)
		right := opsQuery(t, strings.Join(words, " & "))
		got, err := a.And(ctx, right)
		if offset == 1048576 {
			codecError(t, err, "54000", "tsquery is too large")
		} else if err != nil || got == nil {
			t.Fatal(err)
		}
	}
	if queryTooBig(1, 0) || !queryTooBig(1, 1<<30) || !queryTooBig(1<<30, 0) || queryTooBig((1<<30-9)/12, 0) {
		t.Fatal("allocation arithmetic guard")
	}
}
func TestFTSOpsCancellation(t *testing.T) {
	background := context.Background()
	q := opsQuery(t, strings.Repeat("a & ", 2047)+"b")
	original := append([]queryNode(nil), q.nodes...)
	for _, phase := range []struct {
		name  string
		after int
	}{{"size", 3}, {"arena-copy", 35}, {"render", 68}} {
		t.Run(phase.name, func(t *testing.T) {
			ctx := &countingContext{Context: background, after: phase.after}
			got, err := q.And(ctx, q)
			if got != nil || !errors.Is(err, context.Canceled) || ctx.calls < phase.after {
				t.Fatal(got, err, ctx.calls)
			}
		})
	}
	checks := (len(q.nodes) + 255) / 256
	for _, after := range []int{3, 1 + checks + 2} {
		ctx := &countingContext{Context: background, after: after}
		_, err := q.Contains(ctx, q)
		if !errors.Is(err, context.Canceled) {
			t.Fatal("containment post-entry", err)
		}
	}
	words := make([]string, 512)
	for i := range words {
		words[i] = fmt.Sprintf("w%04d:1B", i)
	}
	v := opsVector(t, strings.Join(words, " "))
	before := opsVector(t, strings.Join(words, " "))
	count := &countingContext{Context: background, after: int(^uint(0) >> 1)}
	if _, err := v.Concat(count, v); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []struct {
		name  string
		after int
	}{{"global-max", 3}, {"lexeme-merge", 1 + len(words) + 2 + 3}, {"vector-render", count.calls - 10}} {
		t.Run(phase.name, func(t *testing.T) {
			ctx := &countingContext{Context: background, after: phase.after}
			got, err := v.Concat(ctx, v)
			if got != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(got, err)
			}
		})
	}
	canceled, cancel := context.WithCancel(background)
	cancel()
	for _, op := range []func(context.Context) (*Query, error){func(c context.Context) (*Query, error) { return q.And(c, q) }, q.Not, func(c context.Context) (*Query, error) { return q.Phrase(c, q, 1) }} {
		if _, err := op(canceled); !errors.Is(err, context.Canceled) {
			t.Fatal("entry", err)
		}
	}
	if _, err := q.Contains(canceled, q); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := v.Concat(canceled, v); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	same, err := v.Equal(background, before)
	if err != nil || !same || !reflect.DeepEqual(original, q.nodes) {
		t.Fatal("cancellation mutated sources", err)
	}
}
