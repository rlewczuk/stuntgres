package functions

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestLevenshteinDistance(t *testing.T) {
	for _, tc := range []struct {
		source, target string
		want           int32
	}{
		{"kitten", "sitting", 3}, {"GUMBO", "GAMBOL", 2}, {"abc", "abc", 0},
		{"", "", 0}, {"", "abc", 3}, {"abc", "", 3}, {"ab", "ba", 2},
	} {
		got, err := LevenshteinDistance(context.Background(), tc.source, tc.target, 1, 1, 1, -1)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	// The normal hot path owns only fixed local rows and rune buffers.
	if n := testing.AllocsPerRun(100, func() {
		if _, err := LevenshteinDistance(context.Background(), "kitten", "sitting", 1, 1, 1, 3); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatal("distance allocations", n)
	}
}

func TestLevenshteinWeights(t *testing.T) {
	for _, tc := range []struct {
		source, target      string
		ins, del, sub, want int32
	}{
		{"GUMBO", "GAMBOL", 2, 1, 1, 3}, {"a", "b", 2, 3, 7, 5},
		{"abc", "adc", 2, 3, 4, 4}, {"", "abc", 2, 3, 7, 6}, {"abc", "", 2, 3, 7, 9},
		{"abc", "xyz", 0, 0, 0, 0}, {"a", "b", 1, 1, -4, -4}, {"a", "a", -1, -1, 1, -2},
	} {
		got, err := LevenshteinDistance(context.Background(), tc.source, tc.target, tc.ins, tc.del, tc.sub, -1)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	types := formatUnitTypes()
	text, integer := types[sqltype.Text], types[sqltype.Int4]
	var order []int
	one, _ := sqltype.Integer(integer, 1)
	args := []expr.Expr{
		binaryUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0},
		binaryUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "b"), order: &order, index: 1},
	}
	for i := 2; i < 6; i++ {
		args = append(args, binaryUnitArgument{Info: expr.Info{Usage: integer.Usage()}, value: one, order: &order, index: i})
	}
	call, err := NewLevenshtein(expr.Info{Usage: integer.Usage()}, "levenshtein_less_equal", args)
	if err != nil {
		t.Fatal(err)
	}
	value, err := call.Eval(&binaryUnitContext{Context: context.Background()})
	if err != nil || !value.IsNull() || value.Type() != integer || !reflect.DeepEqual(order, []int{0, 1, 2, 3, 4, 5}) {
		t.Fatal(value, order, err)
	}
	failure := errors.New("late demanded cost")
	args[5] = binaryUnitArgument{Info: expr.Info{Usage: integer.Usage()}, err: failure, order: &order, index: 5}
	order = nil
	if _, err := call.Eval(&binaryUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1, 2, 3, 4, 5}) {
		t.Fatal(order, err)
	}
	for _, tc := range []struct {
		name string
		ids  []sqltype.ID
		want Resolution
	}{
		{"levenshtein", []sqltype.ID{0, 0}, Resolved},
		{"levenshtein", []sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Int4, sqltype.Int4, sqltype.Int4}, Resolved},
		{"levenshtein", []sqltype.ID{sqltype.Int4, sqltype.Text}, Missing},
		{"levenshtein", []sqltype.ID{0, 0, sqltype.Int8, sqltype.Int4, sqltype.Int4}, Missing},
		{"levenshtein_less_equal", []sqltype.ID{0, 0, sqltype.Numeric}, Missing},
		{"levenshtein_less_equal", []sqltype.ID{0, 0, 0}, Resolved},
		{"levenshtein", []sqltype.ID{0, 0, 0}, Missing},
	} {
		arguments := make([]Argument, len(tc.ids))
		for i, id := range tc.ids {
			if id != 0 {
				arguments[i].Usage = types[id].Usage()
			}
		}
		if got := LevenshteinInput(tc.name, arguments, text.Usage(), integer.Usage()); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

func TestLevenshteinBounds(t *testing.T) {
	for _, bound := range []int32{-2147483648, -1, 3, 4, 2147483647} {
		got, err := LevenshteinDistance(context.Background(), "kitten", "sitting", 1, 1, 1, bound)
		if err != nil || got != 3 {
			t.Fatal(bound, got, err)
		}
	}
	for _, bound := range []int32{0, 1, 2} {
		got, err := LevenshteinDistance(context.Background(), "kitten", "sitting", 1, 1, 1, bound)
		if err != nil || got <= bound {
			t.Fatal(bound, got, err)
		}
	}
	for _, tc := range []struct{ bound, want int32 }{{-1, -8}, {0, 1}} {
		got, err := LevenshteinDistance(context.Background(), "a", "aa", 1, -10, 1, tc.bound)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}

func TestLevenshteinReleaseInt32(t *testing.T) {
	for _, tc := range []struct {
		source, target      string
		ins, del, sub, want int32
	}{
		{"", "aa", 2147483647, 1, 1, -2}, {"aa", "", 1, 2147483647, 1, -2},
		{"a", "b", 2147483647, 1, 1, -2147483648},
	} {
		got, err := LevenshteinDistance(context.Background(), tc.source, tc.target, tc.ins, tc.del, tc.sub, -1)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}

func TestLevenshteinUnicode(t *testing.T) {
	for _, tc := range []struct {
		source, target string
		want           int32
	}{
		{"é", "e", 1}, {"é", "e\u0301", 2}, {"🙂", "🙃", 1},
		{strings.Repeat("é", 255), strings.Repeat("é", 255), 0},
		{strings.Repeat("é", 256), "", 256}, {"", strings.Repeat("é", 256), 256},
	} {
		got, err := LevenshteinDistance(context.Background(), tc.source, tc.target, 1, 1, 1, -1)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, pair := range [][2]string{{strings.Repeat("é", 256), "a"}, {"a", strings.Repeat("é", 256)}} {
		_, err := LevenshteinDistance(context.Background(), pair[0], pair[1], 1, 1, 1, 0)
		var state *sqlstate.Error
		if !errors.As(err, &state) || state.Code != "22023" || state.Message != "levenshtein argument exceeds maximum length of 255 characters" {
			t.Fatal(err)
		}
	}
}

func TestLevenshteinCancellation(t *testing.T) {
	for _, after := range []int{1, 2, 4} {
		ctx := &cryptoCancelContext{Context: context.Background(), after: after}
		if _, err := LevenshteinDistance(ctx, strings.Repeat("a", 255), strings.Repeat("b", 255), 1, 1, 1, -1); !errors.Is(err, context.Canceled) {
			t.Fatal(after, err)
		}
	}
}
