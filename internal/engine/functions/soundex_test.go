package functions

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestSoundexKernel(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"hello world!", "H464"}, {"Ann", "A500"}, {"Anne", "A500"}, {"Andrew", "A536"}, {"Margaret", "M626"},
		{"Robert", "R163"}, {"Rupert", "R163"}, {"Rubin", "R150"}, {"Smith", "S530"}, {"Smyth", "S530"},
		{"Ashcraft", "A226"}, {"Ashcroft", "A226"}, {"Pfister", "P236"}, {"Tymczak", "T522"}, {"Honeyman", "H555"},
		{"B", "B000"}, {"BB", "B000"}, {"BHB", "B100"}, {"BWB", "B100"}, {"B-b", "B100"}, {"B1B", "B000"}, {"B2B", "B100"}, {"D3D", "D000"}, {"BéB", "B100"},
		{"", ""}, {"!?123", ""}, {"東京🙂", ""}, {"é", ""}, {"Émile", "M400"}, {"Emile", "E540"},
		{strings.Repeat("a", 255), "A000"}, {strings.Repeat("a", 256), "A000"},
		{strings.Repeat("!", 65536) + "Ann", "A500"}, {strings.Repeat("é", 32768), ""}, {"A" + strings.Repeat("a!", 32768), "A000"},
	} {
		got, err := SoundexCode(context.Background(), tc.input)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, tc := range []struct { left, right string; want int32 }{
		{"Anne", "Ann", 4}, {"Anne", "Andrew", 2}, {"Anne", "Margaret", 0}, {"", "", 4}, {"", "!?", 4}, {"", "A", 0}, {"A", "B", 3},
	} {
		got, err := SoundexDifference(context.Background(), tc.left, tc.right)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, input := range []string{strings.Repeat("!", 16384), "A" + strings.Repeat("a!", 16384)} {
		for _, after := range []int{1, 2, 4, 8} {
			ctx := &cryptoCancelContext{Context: context.Background(), after: after}
			if _, err := SoundexCode(ctx, input); !errors.Is(err, context.Canceled) {
				t.Fatal("bounded scan cancellation", after, err)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SoundexDifference(ctx, "", ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// A score uses fixed buffers, never temporary output strings/input copies.
	input := strings.Repeat("a", 65536)
	if n := testing.AllocsPerRun(10, func() { _, _ = SoundexDifference(context.Background(), input, input) }); n != 0 {
		t.Fatal("difference allocations", n)
	}
}

func TestSoundexExpression(t *testing.T) {
	types := formatUnitTypes()
	text, integer := types[sqltype.Text], types[sqltype.Int4]
	for _, tc := range []struct { name string; count int; signature string }{{"soundex", 1, "25"}, {"difference", 2, "25,25"}, {"text_soundex", 1, ""}, {"soundex", 2, ""}, {"difference", 1, ""}} {
		if got := SoundexSignature(tc.name, tc.count); got != tc.signature {
			t.Fatal(tc, got)
		}
	}
	if SoundexInput("soundex", []Argument[struct{}]{{Usage: integer.Usage()}}, text.Usage()) != Missing {
		t.Fatal("implicit integer-to-text overload admitted")
	}
	var order []int
	args := []binaryUnitExpression{
		binaryUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0},
		binaryUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.String(text, "Ann"), order: &order, index: 1},
	}
	call, err := NewSoundex[binaryUnitExpression, *binaryUnitContext](Info[struct{}]{Usage: integer.Usage()}, "difference", args)
	if err != nil {
		t.Fatal(err)
	}
	value, err := call.Eval(&binaryUnitContext{Context: context.Background()})
	if err != nil || !value.IsNull() || value.Type() != integer || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(value, order, err)
	}
	failure := errors.New("demanded sibling")
	args[1] = binaryUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, err: failure, order: &order, index: 1}
	order = nil
	if _, err := call.Eval(&binaryUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
	if _, err := call.Reconstruct(call.Info, args[:1]); err == nil {
		t.Fatal("invalid arity reconstruction")
	}
	if _, err := call.Reconstruct(Info[struct{}]{Usage: text.Usage()}, args); err == nil {
		t.Fatal("invalid result reconstruction")
	}
}
