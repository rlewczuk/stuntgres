package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/functions"
)

func TestDictIntKernel(t *testing.T) {
	for _, tc := range []struct {
		maxlen             int
		rejectlong, absval bool
		input              string
		want               []fts.Lexeme
	}{
		{6, false, false, "123456", []fts.Lexeme{{Text: "123456"}}},
		{6, false, false, "1234567", []fts.Lexeme{{Text: "123456"}}},
		{6, false, false, "-123456", []fts.Lexeme{{Text: "-12345"}}},
		{3, false, false, "-123", []fts.Lexeme{{Text: "-12"}}},
		{3, false, true, "-123", []fts.Lexeme{{Text: "123"}}},
		{3, false, true, "+123", []fts.Lexeme{{Text: "123"}}},
		{3, true, true, "-123", []fts.Lexeme{{Text: "123"}}},
		{3, true, true, "-1234", nil},
		{3, true, false, "123456", nil},
		{1, false, false, "-9", []fts.Lexeme{{Text: "-"}}},
		{1, false, true, "-9", []fts.Lexeme{{Text: "9"}}},
		{6, false, false, "0000009", []fts.Lexeme{{Text: "000000"}}},
		{6, false, false, "ABCdefghi", []fts.Lexeme{{Text: "ABCdef"}}},
		{6, false, false, "", []fts.Lexeme{{Text: ""}}},
		{6, false, true, "+", []fts.Lexeme{{Text: ""}}},
		{6, false, true, "-", []fts.Lexeme{{Text: ""}}},
		{3, false, true, "--12", []fts.Lexeme{{Text: "-12"}}},
		{2, false, false, "éé", []fts.Lexeme{{Text: "é"}}},
		{3, false, true, "−12", []fts.Lexeme{{Text: "−"}}},
	} {
		d := functions.NewIntDictionary(tc.maxlen, tc.rejectlong, tc.absval)
		if d.Filtering() || !d.Configured(tc.maxlen, tc.rejectlong, tc.absval) || d.Configured(tc.maxlen+1, tc.rejectlong, tc.absval) || d.Configured(tc.maxlen, !tc.rejectlong, tc.absval) || d.Configured(tc.maxlen, tc.rejectlong, !tc.absval) {
			t.Fatal("immutable effective configuration", tc)
		}
		result, err := d.Lexize(t.Context(), tc.input)
		if err != nil || !result.Recognized || !reflect.DeepEqual(result.Lexemes, tc.want) {
			t.Fatal("rich lexical result", tc, result, err)
		}
		// A caller owns the result vector. Editing it must not mutate the
		// immutable dictionary or another invocation's retained output.
		if len(result.Lexemes) != 0 {
			result.Lexemes[0].Text = "changed by caller"
		}
		again, err := d.Lexize(t.Context(), tc.input)
		if err != nil || !reflect.DeepEqual(again.Lexemes, tc.want) {
			t.Fatal("invocations shared mutable lexeme backing", again, err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if result, err := d.Lexize(ctx, tc.input); !errors.Is(err, context.Canceled) || result.Recognized || len(result.Lexemes) != 0 {
			t.Fatal("canceled kernel published a result", result, err)
		}
	}
}
