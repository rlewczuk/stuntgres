package parser

import (
	"reflect"
	"strings"
	"testing"
)

func TestRegclassQualifiedNameInputBoundary(t *testing.T) {
	for _, tc := range []struct{ input string; parts []string }{
		{"BASE--suffix", []string{"base--suffix"}},
		{"base/*suffix*/", []string{"base/*suffix*/"}},
		{"s-other", []string{"s-other"}},
		{" \t123\n", []string{"123"}},
		{" - ", []string{"-"}},
		{` "Mixed.Schema" . "a""b.dot" `, []string{"Mixed.Schema", `a"b.dot`}},
		{`""`, []string{""}},
		{`s"quote`, []string{`s"quote`}},
		{"s\u00a0suffix", []string{"s\u00a0suffix"}},
		{strings.Repeat("界", 22), []string{strings.Repeat("界", 21)}},
		{`"`+strings.Repeat("界", 22)+`"`, []string{strings.Repeat("界", 21)}},
		{"db . public . s", []string{"db", "public", "s"}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			name, err := ParseRegclassName(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			parts := make([]string, len(name.Parts))
			for i, part := range name.Parts {
				parts[i] = part.Text
			}
			if !reflect.DeepEqual(parts, tc.parts) {
				t.Fatal(parts, tc.parts)
			}
		})
	}
	for _, input := range []string{"", " \t\n", ".s", "s.", "s..t", "s trailing", `"s"suffix`, `"s`, "a.b.c.d"} {
		if name, err := ParseRegclassName(input); err == nil {
			t.Errorf("accepted incomplete or invalid input %q: %#v", input, name)
		}
	}
}

func TestRegclassQualifiedNameInputPositions(t *testing.T) {
	name, err := ParseRegclassName(" \t界 . \"S\"")
	if err != nil {
		t.Fatal(err)
	}
	if len(name.Parts) != 2 || name.Parts[0].Pos != (Position{Offset: 2, Character: 3}) || name.Parts[1].Pos != (Position{Offset: 8, Character: 7}) {
		t.Fatal(name)
	}
}
