package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestJSONPathScannerDecodedTokensAndSpans(t *testing.T) {
	text := `strict $a."x y"[0xA_b,1.2]`
	s := jsonpathScanner{ctx: context.Background(), text: text}
	cases := []struct {
		kind, text, raw string
		start, end      int
	}{
		{"strict", "strict", " ", 0, 6},
		{"variable", "a", "$a", 7, 9},
		{".", ".", ".", 9, 10},
		{"quoted", "x y", `"`, 10, 15},
		{"[", "[", "[", 15, 16},
		{"int", "0xA_b", "0xA_b", 16, 21},
		{",", ",", ",", 21, 22},
		{"numeric", "1.2", "1.2", 22, 25},
		{"]", "]", "]", 25, 26},
		{"eof", "", "", 26, 26},
	}
	for _, want := range cases {
		got, err := s.next()
		if err != nil || got.kind != want.kind || got.text != want.text || got.raw != want.raw || got.start != want.start || got.end != want.end {
			t.Fatal(got, want, err)
		}
	}
	for _, tc := range []struct{ input, kind, text string }{{`\u0061`, "name", "a"}, {`"\z"`, "quoted", "z"}, {"STRICT", "strict", "STRICT"}, {"True", "name", "True"}, {"true", "true", "true"}, {"Keyvalue", "name", "Keyvalue"}} {
		scanner := jsonpathScanner{ctx: context.Background(), text: tc.input}
		token, err := scanner.next()
		if err != nil || token.kind != tc.kind || token.text != tc.text {
			t.Fatal(token, tc, err)
		}
	}
	for _, tc := range []struct{ input, raw string }{{"1.type()", "1.t"}, {"1a", "1a"}, {"01", "01"}, {"1_", "1_"}} {
		scanner := jsonpathScanner{ctx: context.Background(), text: tc.input}
		_, err := scanner.next()
		var e *sqlstate.Error
		if !errors.As(err, &e) || e.Message != `trailing junk after numeric literal at or near "`+tc.raw+`" of jsonpath input` {
			t.Fatal(tc, err)
		}
	}
}
