package functions

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestMetaphoneSignatures(t *testing.T) {
	types := formatUnitTypes()
	for _, tc := range []struct {
		name      string
		ids       []sqltype.ID
		signature string
		want      Resolution
	}{
		{"metaphone", []sqltype.ID{0, 0}, "25,23", Resolved},
		{"metaphone", []sqltype.ID{sqltype.Text, sqltype.Int2}, "25,23", Resolved},
		{"metaphone", []sqltype.ID{sqltype.Text, sqltype.Int8}, "25,23", Missing},
		{"metaphone", []sqltype.ID{sqltype.Text, sqltype.Numeric}, "25,23", Missing},
		{"metaphone", []sqltype.ID{sqltype.Int4, sqltype.Int4}, "25,23", Missing},
		{"metaphone", []sqltype.ID{0}, "", Missing},
		{"dmetaphone", []sqltype.ID{0}, "25", Resolved},
		{"dmetaphone_alt", []sqltype.ID{sqltype.Text}, "25", Resolved},
		{"dmetaphone_alt", []sqltype.ID{sqltype.Int4}, "25", Missing},
		{"dmetaphone", []sqltype.ID{0, 0}, "", Missing},
	} {
		args := make([]Argument[struct{}], len(tc.ids))
		for i, id := range tc.ids {
			if id != 0 {
				args[i].Usage = types[id].Usage()
			}
		}
		if got := MetaphoneSignature(tc.name, len(args)); got != tc.signature {
			t.Fatal(tc, got)
		}
		if got := MetaphoneInput(tc.name, args, types[sqltype.Text].Usage(), types[sqltype.Int4].Usage()); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

func TestMetaphoneCodes(t *testing.T) {
	for _, tc := range []struct {
		source string
		cap    int32
		want   string
	}{
		{"GUMBO", 4, "KM"}, {"Smith", 8, "SM0"}, {"Schmidt", 8, "SKMTT"}, {"smith", 8, "SM0"},
		{"phone", 8, "FN"}, {"whale", 8, "HL"}, {"christ", 8, "KRST"}, {"school", 8, "SKL"}, {"Schwartz", 8, "XWRTS"},
		{"AE", 8, "E"}, {"!!AE", 8, "E"}, {"X", 8, "S"}, {"AX", 1, "A"}, {"AX", 2, "AK"}, {"AX", 3, "AKS"},
		{"ch", 8, "X"}, {"c-h", 8, "K"}, {"éa", 8, "A"}, {"123!?", 8, ""},
	} {
		got, err := MetaphoneCode(context.Background(), tc.source, tc.cap)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}
func TestMetaphoneBoundaries(t *testing.T) {
	for _, cap := range []int32{-2147483648, -1, 0, 256, 2147483647} {
		got, err := MetaphoneCode(context.Background(), "", cap)
		if err != nil || got != "" {
			t.Fatal(cap, got, err)
		}
	}
	for _, tc := range []struct {
		source        string
		cap           int32
		code, message string
	}{
		{strings.Repeat("a", 256), 0, "22023", "argument exceeds the maximum length of 255 bytes"},
		{strings.Repeat("é", 128), 256, "22023", "argument exceeds the maximum length of 255 bytes"},
		{"!", 256, "22023", "output exceeds the maximum length of 255 bytes"},
		{"a", 0, "2200F", "output cannot be empty string"},
		{"!", -1, "2200F", "output cannot be empty string"},
	} {
		_, err := MetaphoneCode(context.Background(), tc.source, tc.cap)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Position != "" {
			t.Fatal(tc, err)
		}
	}
	got, err := MetaphoneCode(context.Background(), strings.Repeat("a", 255), 255)
	if err != nil || got != "A" {
		t.Fatal(got, err)
	}
	got, err = MetaphoneCode(context.Background(), strings.Repeat("é", 127), 4)
	if err != nil || got != "" {
		t.Fatal(got, err)
	}
}
func TestMetaphoneStrictDemand(t *testing.T) {
	types := formatUnitTypes()
	text, integer := types[sqltype.Text], types[sqltype.Int4]
	var order []int
	cap, _ := sqltype.Integer(integer, 4)
	args := []binaryUnitExpression{
		binaryUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0},
		binaryUnitArgument{Info: Info[struct{}]{Usage: integer.Usage()}, value: cap, order: &order, index: 1},
	}
	call, err := NewMetaphone[binaryUnitExpression, *binaryUnitContext](Info[struct{}]{Usage: text.Usage()}, "metaphone", args)
	if err != nil {
		t.Fatal(err)
	}
	value, err := call.Eval(&binaryUnitContext{Context: context.Background()})
	if err != nil || !value.IsNull() || value.Type() != text || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(value, order, err)
	}
	failure := errors.New("demanded cap error")
	args[1] = binaryUnitArgument{Info: Info[struct{}]{Usage: integer.Usage()}, err: failure, order: &order, index: 1}
	order = nil
	if _, err := call.Eval(&binaryUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
	if _, err := call.Reconstruct(call.Info, args[:1]); err == nil {
		t.Fatal("invalid reconstruction accepted")
	}
}
func TestMetaphoneCancellation(t *testing.T) {
	for _, after := range []int{1, 2, 4} {
		ctx := &cryptoCancelContext{Context: context.Background(), after: after}
		if _, err := MetaphoneCode(ctx, strings.Repeat("a", 255), 4); !errors.Is(err, context.Canceled) {
			t.Fatal(after, err)
		}
	}
}

func TestDoubleMetaphoneCodes(t *testing.T) {
	for _, tc := range []struct{ source, primary, alternate string }{
		{"gumbo", "KMP", "KMP"}, {"Smith", "SM0", "XMT"}, {"Schmidt", "XMT", "SMT"}, {"", "", ""},
		{"a", "A", "A"}, {"y", "A", "A"}, {"x", "S", "S"}, {"j", "J", "A"}, {"aj", "AJ", "A"},
		{"Jackson", "JKSN", "AKSN"}, {"Jose", "HS", "HS"}, {"Josef", "JSF", "HSF"}, {"Rogier", "RJ", "RJR"}, {"Washington", "AXNK", "FXNK"},
		{"123!?", "", ""}, {"éa", "", ""}, {"Ç", "", ""}, {"Ñ", "", ""},
	} {
		primary, alternate, err := DoubleMetaphone(context.Background(), tc.source)
		if err != nil || primary != tc.primary || alternate != tc.alternate {
			t.Fatal(tc, primary, alternate, err)
		}
	}
}
func TestDoubleMetaphoneBoundaries(t *testing.T) {
	for _, tc := range []struct{ source, want string }{{strings.Repeat("a", 300), "A"}, {strings.Repeat("b", 300), "PPPP"}} {
		p, a, err := DoubleMetaphone(context.Background(), tc.source)
		if err != nil || p != tc.want || a != tc.want {
			t.Fatal(p, a, err)
		}
	}
	// A Germanic marker beyond any consumed code prefix still changes SIO.
	for _, tc := range []struct{ suffix, alternate string }{{"", "X"}, {"W", "S"}} {
		p, a, err := DoubleMetaphone(context.Background(), "SIO"+strings.Repeat("!", 300)+tc.suffix)
		if err != nil || p != "S" || a != tc.alternate {
			t.Fatal("full-input classification", p, a, err)
		}
	}
	d := doubleMetaphoneState{source: "jose"}
	if d.at(-1) != 0 || d.at(0) != 'J' || d.at(3) != 'E' || d.at(4) != ' ' || d.at(8) != ' ' || d.at(9) != 0 || !d.is(0, "JOSE ") {
		t.Fatal("virtual release padding")
	}
	d.add("ABCDE", "X")
	d.add("F", "YZ")
	if string(d.primary[:d.pn]) != "ABCD" || string(d.alternate[:d.an]) != "XYZ" {
		t.Fatal("independent saturation", d)
	}
}
func TestDoubleMetaphoneCancellation(t *testing.T) {
	for _, after := range []int{1, 2, 4, 8} {
		ctx := &cryptoCancelContext{Context: context.Background(), after: after}
		if _, _, err := DoubleMetaphone(ctx, strings.Repeat("!", 8192)); !errors.Is(err, context.Canceled) {
			t.Fatal(after, err)
		}
	}
}
func TestDoubleMetaphoneAllocations(t *testing.T) {
	// Calibrated per-call allocation excludes input creation and unrelated runtime
	// activity; long borrowed input must never allocate an uppercase/padded copy.
	source := strings.Repeat("a", 16384)
	var primary, alternate string
	result := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			var err error
			primary, alternate, err = DoubleMetaphone(context.Background(), source)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	if primary != "A" || alternate != "A" || result.AllocedBytesPerOp() > 32 || result.AllocsPerOp() > 2 {
		t.Fatal(primary, alternate, result.AllocedBytesPerOp(), result.AllocsPerOp())
	}
}
