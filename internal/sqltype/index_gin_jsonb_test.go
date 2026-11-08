package sqltype

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type ginJSONBWalkContext struct {
	context.Context
	remaining int
}

func (c *ginJSONBWalkContext) Err() error {
	if c.remaining <= 0 {
		return context.Canceled
	}
	c.remaining--
	return nil
}

func ginJSONBTestBinding(t *testing.T, jsonb *Descriptor, name string) GinBinding {
	t.Helper()
	class, ok := LookupGinIndexClass(name)
	if !ok {
		t.Fatal("missing jsonb GIN class", name)
	}
	binding, err := BindGinTokens(jsonb.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func ginJSONBValue(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func ginJSONBLexemes(t *testing.T, binding GinBinding, value Value) []string {
	t.Helper()
	var lexemes []string
	if err := binding.Extract(context.Background(), value, func(token GinToken) error {
		if token.Kind != GinOrdinary || token.Element.typ != nil {
			t.Fatalf("jsonb emitted a non-ordinary or non-lexeme token: %+v", token)
		}
		lexemes = append(lexemes, token.Lexeme)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(lexemes)
	return lexemes
}

func TestGINJSONBTokens(t *testing.T) {
	jsonb := testTypes()[JSONB]
	ops := ginJSONBTestBinding(t, jsonb, "jsonb_ops")
	path := ginJSONBTestBinding(t, jsonb, "jsonb_path_ops")
	if !ops.IsJSONB() || ops.JSONBPathOps() || ops.IsScalar() {
		t.Fatal("jsonb_ops discriminator", ops)
	}
	if !path.IsJSONB() || !path.JSONBPathOps() || path.IsScalar() {
		t.Fatal("jsonb_path_ops discriminator", path)
	}
	for _, tc := range []struct {
		text   string
		tokens []string
	}{
		{`{"a":"b"}`, []string{"\x01a", "\x05b"}},
		{`{"a":null}`, []string{"\x01a", "\x02"}},
		{`{"b":true,"c":false}`, []string{"\x01b", "\x01c", "\x03f", "\x03t"}},
		{`["a",1]`, []string{"\x01a", "\x041"}},
		{`{"o":{"k":"v"}}`, []string{"\x01k", "\x01o", "\x05v"}},
		{`"foo"`, []string{"\x01foo"}},
		{`1`, []string{"\x041"}},
		{`{"":1}`, []string{"\x01", "\x041"}},
		{`{"a":{"b":{"c":1}}}`, []string{"\x01a", "\x01b", "\x01c", "\x041"}},
		{`{"a":["s"]}`, []string{"\x01a", "\x01s"}},
		{`{}`, nil},
		{`[]`, nil},
		{`null`, []string{"\x02"}},
	} {
		value := ginJSONBValue(t, jsonb, tc.text)
		if got := ginJSONBLexemes(t, ops, value); !slices.Equal(got, tc.tokens) {
			t.Fatal("jsonb_ops tokens", tc.text, got, tc.tokens)
		}
	}
	null := Null(jsonb)
	var nullTokens []GinToken
	if err := ops.Extract(context.Background(), null, func(token GinToken) error {
		nullTokens = append(nullTokens, token)
		return nil
	}); err != nil || len(nullTokens) != 1 || nullTokens[0].Kind != GinNullItem {
		t.Fatal("SQL NULL stays GinNullItem", nullTokens, err)
	}

	// numeric_normalize: equal spellings share a payload, no display-scale leak.
	equal := []string{`1`, `1.0`, `1e0`}
	var first []string
	for i, text := range equal {
		got := ginJSONBLexemes(t, ops, ginJSONBValue(t, jsonb, text))
		if i == 0 {
			first = got
		} else if !slices.Equal(got, first) {
			t.Fatal("numeric equivalence", text, got, first)
		}
	}
	if !slices.Equal(first, []string{"\x041"}) {
		t.Fatal("numeric payload", first)
	}
	if got := ginJSONBLexemes(t, ops, ginJSONBValue(t, jsonb, `1.230e-5`)); !slices.Equal(got, []string{"\x040.0000123"}) {
		t.Fatal("fraction normalization", got)
	}
	for _, text := range []string{`0`, `-0`, `0.00`} {
		if got := ginJSONBLexemes(t, ops, ginJSONBValue(t, jsonb, text)); !slices.Equal(got, []string{"\x040"}) {
			t.Fatal("zero normalization", text, got)
		}
	}

	// Long payloads are replaced by eight lowercase hex digits plus HASHED.
	long := strings.Repeat("x", 130)
	hashed := ginJSONBLexemes(t, ops, ginJSONBValue(t, jsonb, `"`+long+`"`))
	again := ginJSONBLexemes(t, ops, ginJSONBValue(t, jsonb, `"`+long+`"`))
	if len(hashed) != 1 || !slices.Equal(hashed, again) || len(hashed[0]) != 9 || hashed[0][0] != 0x01|0x10 || strings.Trim(hashed[0][1:], "0123456789abcdef") != "" {
		t.Fatal("long key hashed category", hashed, again)
	}
	distinct := ginJSONBLexemes(t, ops, ginJSONBValue(t, jsonb, `"`+strings.Repeat("y", 130)+`"`))
	if slices.Equal(hashed, distinct) {
		t.Fatal("distinct long payloads collided")
	}

	// jsonb_path_ops: one 4-byte hash per scalar leaf; arrays add no path.
	for _, tc := range []struct {
		text  string
		count int
	}{
		{`{"a":{}}`, 0}, {`{"a":[]}`, 0}, {`{"a":[1,2]}`, 2},
		{`{}`, 0}, {`[]`, 0}, {`null`, 1}, {`1`, 1},
	} {
		got := ginJSONBLexemes(t, path, ginJSONBValue(t, jsonb, tc.text))
		if len(got) != tc.count {
			t.Fatal("path token count", tc.text, got)
		}
		for _, lexeme := range got {
			if len(lexeme) != 4 {
				t.Fatal("path token width", tc.text, lexeme)
			}
		}
	}
	one := ginJSONBLexemes(t, path, ginJSONBValue(t, jsonb, `1`))
	onePointZero := ginJSONBLexemes(t, path, ginJSONBValue(t, jsonb, `1.0`))
	if !slices.Equal(one, onePointZero) {
		t.Fatal("path numeric equivalence", one, onePointZero)
	}
	if slices.Equal(ginJSONBLexemes(t, path, ginJSONBValue(t, jsonb, `{"a":1}`)), ginJSONBLexemes(t, path, ginJSONBValue(t, jsonb, `{"b":1}`))) {
		t.Fatal("path hash ignored the key")
	}
}

func ginJSONBQueryTokens(t *testing.T, binding GinBinding, strategy int16, value Value) ([]string, bool) {
	t.Helper()
	var lexemes []string
	allNonNull, err := binding.ExtractJSONBQuery(context.Background(), strategy, value, func(token GinToken) error {
		if token.Kind != GinOrdinary || token.Element.typ != nil {
			t.Fatalf("jsonb query emitted a foreign token: %+v", token)
		}
		lexemes = append(lexemes, token.Lexeme)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(lexemes)
	return lexemes, allNonNull
}

func TestGINJSONBQueries(t *testing.T) {
	jsonb := testTypes()[JSONB]
	ops := ginJSONBTestBinding(t, jsonb, "jsonb_ops")
	path := ginJSONBTestBinding(t, jsonb, "jsonb_path_ops")
	types := testTypes()
	text, textArray, jsonpath := types[Text], types[1009], types[JSONPath]
	if text == nil || textArray == nil || jsonpath == nil {
		t.Fatal("missing operand types")
	}
	contains := ginJSONBValue(t, jsonb, `{"a":"b"}`)
	want, all := ginJSONBQueryTokens(t, ops, 7, contains)
	if all || !slices.Equal(want, ginJSONBLexemes(t, ops, contains)) {
		t.Fatal("strategy 7 tokens", want, all)
	}
	if got, all := ginJSONBQueryTokens(t, ops, 7, ginJSONBValue(t, jsonb, `{}`)); !all || len(got) != 0 {
		t.Fatal("empty containment sets ALL", got, all)
	}
	if got, all := ginJSONBQueryTokens(t, ops, 9, ginJSONBValue(t, text, "a")); all || !slices.Equal(got, []string{"\x01a"}) {
		t.Fatal("strategy 9", got, all)
	}
	array := ginJSONBValue(t, textArray, `{a,NULL,b}`)
	for _, strategy := range []int16{10, 11} {
		got, all := ginJSONBQueryTokens(t, ops, strategy, array)
		if all || !slices.Equal(got, []string{"\x01a", "\x01b"}) {
			t.Fatal("array text strategy", strategy, got, all)
		}
	}
	if got, all := ginJSONBQueryTokens(t, ops, 10, ginJSONBValue(t, textArray, `{}`)); all || len(got) != 0 {
		t.Fatal("empty ?| matches nothing", got, all)
	}
	if got, all := ginJSONBQueryTokens(t, ops, 11, ginJSONBValue(t, textArray, `{}`)); !all || len(got) != 0 {
		t.Fatal("empty ?& matches all non-null", got, all)
	}
	for _, strategy := range []int16{15, 16} {
		if got, all := ginJSONBQueryTokens(t, ops, strategy, ginJSONBValue(t, jsonpath, `$.a`)); !all || len(got) != 0 {
			t.Fatal("conservative jsonpath", strategy, got, all)
		}
	}
	for _, strategy := range []int16{7, 9, 10, 11, 15, 16} {
		if got, all := ginJSONBQueryTokens(t, ops, strategy, Null(jsonb)); all || len(got) != 0 {
			t.Fatal("SQL NULL query", strategy, got, all)
		}
	}
	for _, tc := range []struct {
		binding  GinBinding
		strategy int16
		value    Value
	}{{path, 9, ginJSONBValue(t, text, "a")}, {ops, 12, contains}, {path, 7, ginJSONBValue(t, text, "a")}, {ops, 9, contains}, {ops, 7, ginJSONBValue(t, text, "a")}, {ops, 10, ginJSONBValue(t, text, "a")}} {
		if _, err := tc.binding.ExtractJSONBQuery(context.Background(), tc.strategy, tc.value, func(GinToken) error { return nil }); err == nil {
			t.Fatal("invalid jsonb query accepted", tc.binding.Class.Name, tc.strategy, tc.value.Type().Name())
		}
	}
	if _, err := ops.ExtractJSONBQuery(context.Background(), 9, ginJSONBValue(t, text, "a"), func(GinToken) error { return nil }); err != nil {
		t.Fatal(err)
	}
	// The whole-query slot-3 callback is the containment extraction.
	var slotThree []string
	if err := ops.ExtractQueryWithEnvironment(context.Background(), contains, func(token GinToken) error {
		slotThree = append(slotThree, token.Lexeme)
		return nil
	}, InputEnvironment{}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(slotThree)
	if !slices.Equal(slotThree, ginJSONBLexemes(t, ops, contains)) {
		t.Fatal("slot 3 equals strategy 7", slotThree)
	}
}

func TestGINJSONBCompare(t *testing.T) {
	jsonb := testTypes()[JSONB]
	ops := ginJSONBTestBinding(t, jsonb, "jsonb_ops")
	path := ginJSONBTestBinding(t, jsonb, "jsonb_path_ops")
	value := func(lexeme string) GinToken { return GinToken{Lexeme: lexeme} }
	var ordered []GinToken
	for _, lexeme := range []string{"\x01a", "\x01b", "\x02", "\x03f", "\x03t", "\x041", "\x05a"} {
		ordered = append(ordered, value(lexeme))
	}
	for i := 1; i < len(ordered); i++ {
		got, err := ops.Compare(context.Background(), ordered[i-1], ordered[i], InputEnvironment{})
		if err != nil || got >= 0 {
			t.Fatal("jsonb_ops byte order", ordered[i-1].Lexeme, ordered[i].Lexeme, got, err)
		}
		reverse, err := ops.Compare(context.Background(), ordered[i], ordered[i-1], InputEnvironment{})
		if err != nil || reverse <= 0 {
			t.Fatal("jsonb_ops antisymmetry", got, reverse, err)
		}
	}
	if got, err := ops.Compare(context.Background(), ordered[0], ordered[len(ordered)-1], InputEnvironment{}); err != nil || got >= 0 {
		t.Fatal("jsonb_ops transitivity", got, err)
	}
	signed := []string{string([]byte{0x80, 0, 0, 0}), string([]byte{0xff, 0xff, 0xff, 0xff}), string([]byte{0, 0, 0, 0}), string([]byte{0, 0, 0, 1}), string([]byte{0x7f, 0xff, 0xff, 0xff})}
	for i := 1; i < len(signed); i++ {
		got, err := path.Compare(context.Background(), value(signed[i-1]), value(signed[i]), InputEnvironment{})
		if err != nil || got >= 0 {
			t.Fatal("signed int32 path order", i, got, err)
		}
	}
	for _, tc := range []struct {
		binding GinBinding
		a, b    GinToken
	}{{ops, value(""), value("\x01a")}, {path, value("abc"), value("abcd")}, {path, value("abc"), value("abc")}} {
		if _, err := tc.binding.Compare(context.Background(), tc.a, tc.b, InputEnvironment{}); err == nil {
			t.Fatal("provider shape accepted", tc.binding.Class.Name, tc.a, tc.b)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ops.Compare(canceled, ordered[0], ordered[1], InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled comparison", err)
	}
	if err := ops.Extract(canceled, ginJSONBValue(t, jsonb, `{"a":1}`), func(GinToken) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled extraction", err)
	}
	var fields strings.Builder
	fields.WriteByte('{')
	for i := range 200 {
		if i > 0 {
			fields.WriteByte(',')
		}
		fields.WriteString(`"k`)
		fields.WriteString(strconv.Itoa(i))
		fields.WriteByte('"')
		fields.WriteByte(':')
		fields.WriteByte('1')
	}
	fields.WriteByte('}')
	wide := ginJSONBValue(t, jsonb, fields.String())
	for _, remaining := range []int{1, 3, 8, 40} {
		ctx := &ginJSONBWalkContext{Context: context.Background(), remaining: remaining}
		if err := ops.Extract(ctx, wide, func(GinToken) error { return nil }); !errors.Is(err, context.Canceled) {
			t.Fatal("counted walk cancellation", remaining, err)
		}
	}
}