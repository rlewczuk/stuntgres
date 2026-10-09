package reference_test

import "strings"

// Independent Unicode expectations copied from driver_test.go.
// Unicode expectations are literal release specifications, not decoder output.
func unicodeEscapeValues() []struct{ source, text string } {
	return []struct{ source, text string }{
		{`U&'\0041'`, "A"}, {`u&'\+01f600'`, "😀"},
		{`U&'\D83D\DE00'`, "😀"}, {`U&'\+00D83D\+00DE00'`, "😀"},
		{`U&'\D83D\+00DE00'`, "😀"}, {`U&'\+00D83D\DE00'`, "😀"},
		{`U&'\D800\DC00'`, "\U00010000"}, {`U&'\DBFF\DFFF'`, "\U0010FFFF"},
		{`U&'\+10FFFF'`, "\U0010FFFF"}, {`U&'\D7FF\E000\FFFF'`, "\uD7FF\uE000\uFFFF"},
		{`U&'\0001'`, "\x01"}, {`U&'\00410'`, "A0"}, {`U&'\+0000410'`, "A0"},
		{`U&'a\\b'`, `a\b`}, {`U&'O''Brien'`, "O'Brien"},
		{`U&'!0041!+01F600!!\0041' UESCAPE '!'`, `A😀!\0041`},
		{`U&'g0041gg' UESCAPE 'g'`, "Ag"},
		{`U&'/0041//' UESCAPE '/'`, "A/"}, {`U&'_0041__' UESCAPE '_'`, "A_"},
		{`U&'\0041\\' UESCAPE '\'`, `A\`},
		{`U&'!0041' UESCAPE E'!'`, "A"}, {`U&'!0041' UESCAPE $$!$$`, "A"},
		{`U&'!0041' /*a*/ uescape /*b*/ '!'`, "A"}, {`U&''`, ""},
		{"U&'\\D83D'\n'\\DE00'", "😀"}, {"U&'\\00'\r'41'", "A"},
		{"U&'\\00' -- continued\n'41'", "A"}, {`U&'$1'`, "$1"},
	}
}

func unicodeEscapeErrors() []struct{ source, message, position, hint string } {
	const h = `Unicode escapes must be \XXXX or \+XXXXXX.`
	out := []struct{ source, message, position, hint string }{
		{`U&'\061')`, "invalid Unicode escape", "26", h},
		{`U&'\+0061')`, "invalid Unicode escape", "26", h},
		{`U&'\00GG')`, "invalid Unicode escape", "26", h},
		{`U&'\0000')`, "invalid Unicode escape value", "26", ""},
		{`U&'\+000000')`, "invalid Unicode escape value", "26", ""},
		{`U&'\+110000')`, "invalid Unicode escape value", "26", ""},
		{`U&'\DC00')`, "invalid Unicode surrogate pair", "26", ""},
		{`U&'\DFFF')`, "invalid Unicode surrogate pair", "26", ""},
		{`U&'\D800')`, "invalid Unicode surrogate pair", "31", ""},
		{`U&'\DBFF')`, "invalid Unicode surrogate pair", "31", ""},
		{`U&'\D800x')`, "invalid Unicode surrogate pair", "31", ""},
		{`U&'\D800\0041')`, "invalid Unicode surrogate pair", "31", ""},
		{`U&'\D800\D800')`, "invalid Unicode surrogate pair", "31", ""},
		{`U&'\D800\\')`, "invalid Unicode surrogate pair", "31", ""},
		{`U&'\+00D800')`, "invalid Unicode surrogate pair", "34", ""},
		{`U&'\D800\+110000')`, "invalid Unicode escape value", "31", ""},
		{`U&'\D800\061')`, "invalid Unicode escape", "31", h},
		{`U&'a''\061')`, "invalid Unicode escape", "28", h},
		{`U&'a''😀\061')`, "invalid Unicode escape", "30", h},
		{"U&'a'\n'😀x\\061')", "invalid Unicode escape", "31", h},
		{"U&'a'\n'\\061')", "invalid Unicode escape", "27", h},
		{"U&'\\D800'\n'x')", "invalid Unicode surrogate pair", "31", ""},
		{`U&'é\061')`, "invalid Unicode escape", "27", h},
		{`U&'x' UESCAPE +)`, `UESCAPE must be followed by a simple string literal at or near "+"`, "37", ""},
		{`U&'x' UESCAPE`, "UESCAPE must be followed by a simple string literal at end of input", "36", ""},
		{`U&'\061' UESCAPE '+' )`, `invalid Unicode escape character at or near "'+'"`, "40", ""},
		{`U&'\061' /*`, `unterminated /* comment at or near "/*"`, "32", ""},
		{`U&'\061' 'oops`, `unterminated quoted string at or near "'oops"`, "32", ""},
		{`U&'x' UESCAPE U&'\061')`, `UESCAPE must be followed by a simple string literal at or near "U&'\061'"`, "37", ""},
		{`U&'abc`, `unterminated quoted string at or near "U&'abc"`, "23", ""},
		{"U&'a'\n'b", "unterminated quoted string at or near \"U&'a'\n'b\"", "23", ""},
		{`U&'\')`, "invalid Unicode escape", "26", h},
		{`U&'x' UESCAPE E'oops`, `unterminated quoted string at or near "E'oops"`, "37", ""},
		{`U&'x' UESCAPE $$oops`, `unterminated dollar-quoted string at or near "$$oops"`, "37", ""},
	}
	forbidden := []string{"''", "'ab'", "'é'", "'😀'", "'+'", "''''", "'\"'", "' '", `E'\t'`, `E'\n'`, `E'\r'`, `E'\f'`, `E'\v'`}
	for _, c := range "0123456789abcdefABCDEF" {
		forbidden = append(forbidden, "'"+string(c)+"'")
	}
	for _, raw := range forbidden {
		out = append(out, struct{ source, message, position, hint string }{
			"U&'x' UESCAPE " + raw + ")", "invalid Unicode escape character at or near \"" + raw + "\"", "37", "",
		})
	}
	for _, raw := range []string{"$1", "1", `"!"`, `U&"\061"`} {
		out = append(out, struct{ source, message, position, hint string }{
			"U&'x' UESCAPE " + raw + ")", "UESCAPE must be followed by a simple string literal at or near \"" + raw + "\"", "37", "",
		})
	}
	for _, width := range []int{4, 6} {
		for i := range width {
			for _, tail := range []string{"", "G"} {
				escape := `\`
				if width == 6 {
					escape += "+"
				}
				out = append(out, struct{ source, message, position, hint string }{
					"U&'" + escape + strings.Repeat("0", i) + tail + "')", "invalid Unicode escape", "26", h,
				})
			}
		}
	}
	return out
}

func unicodeEscapeNames() []struct{ source, name string } {
	return []struct{ source, name string }{
		{`U&"\0041"`, "A"}, {`U&"select"`, "select"}, {`U&"a\002eb"`, "a.b"},
		{`U&"UESCAPE"`, "UESCAPE"}, {`U&"FIRST"`, "FIRST"}, {`U&"NULLS"`, "NULLS"},
		{`U&"!0042" UESCAPE '!'`, "B"},
		{`U&"a""b\0022c"`, `a"b"c`}, {`U&"\+01F600"`, "😀"},
		{`U&"` + strings.Repeat(`\0061`, 63) + `"`, strings.Repeat("a", 63)},
		{`U&"` + strings.Repeat(`\0062`, 64) + `"`, strings.Repeat("b", 63)},
		{`U&"` + strings.Repeat("c", 62) + `\00e9"`, strings.Repeat("c", 62)},
		{`U&"` + strings.Repeat("d", 59) + `\+01f600"`, strings.Repeat("d", 59) + "😀"},
	}
}

func unicodeEscapeCompositionCases() []struct{ query, code, message, position, hint string } {
	return []struct{ query, code, message, position, hint string }{
		{`SELECT x FROM t ORDER BY x NULLS U&"FIRST"`, "42601", `syntax error at or near "NULLS"`, "28", ""},
	}
}
