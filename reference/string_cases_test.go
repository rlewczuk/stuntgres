package reference_test

import "strings"

// Independent literal expectations copied from driver_test.go.
// These expected values are specified independently of either SQL engine.
func stringLiteralValues() []struct{ source, text string } {
	return []struct{ source, text string }{
		{`E'a\nb'`, "a\nb"}, {`E'O\'Brien\\tail'`, "O'Brien\\tail"},
		{`E'O''Brien'`, "O'Brien"}, {"E'a'\r'\\n'", "a\n"},
		{`E''`, ""}, {`e'\303\251'`, "é"}, {`E'\xC3\xA9'`, "é"},
		{`E'\U0001F600'`, "😀"}, {`E'\uD83D\uDE00'`, "😀"},
		{`E'\uD83D\U0000DE00'`, "😀"}, {`E'\U0000D83D\uDE00'`, "😀"},
		{`E'\b\f\n\r\t\v'`, "\b\f\n\r\t\v"}, {`E'\q\8\9\x\X'`, "q89xX"},
		{`E'\101\12\1\1012\x41\x4\x414'`, "A\n\x01A2A\x04A4"},
		{`E'\u0061\U00000062\u00612'`, "aba2"}, {`E'\uFFFF\uFFFD'`, "\uFFFF\uFFFD"},
		{"E'\\xC3'\n'\\xA9'", "é"}, {"E'a' -- continue\r'\\n'", "a\n"},
		{`'O''Brien\n'`, `O'Brien\n`}, {"'a'\n'\\n'", `a\n`},
		{"E'\\\n\\\r'", "\n\r"},
		{`$$a;'b$$`, "a;'b"}, {`$$$$`, ""}, {`$tag$$tag$`, ""},
		{`$tag$a$TAG$b$tag$`, "a$TAG$b"}, {`$tag$a$junk$tag$`, "a$junk"},
		{`$_9$é😀$_9$`, "é😀"}, {`$é$unicode$é$`, "unicode"},
		{"$Tag_9$first\nsecond; O'Brien\\tail $1 $$ $other$x$other$$Tag_9$", "first\nsecond; O'Brien\\tail $1 $$ $other$x$other$"},
		{"$$/* comment */ -- not comment\n$1; CREATE TABLE phantom(x INT); \\n$$", "/* comment */ -- not comment\n$1; CREATE TABLE phantom(x INT); \\n"},
		{"$" + strings.Repeat("a", 64) + "$long$" + strings.Repeat("a", 64) + "$", "long"},
		{"$" + strings.Repeat("a", 64) + "$a$" + strings.Repeat("a", 63) + "b$$" + strings.Repeat("a", 64) + "$", "a$" + strings.Repeat("a", 63) + "b$"},
	}
}

func stringLiteralErrors() []struct{ source, code, message, position, hint string } {
	const h = `Unicode escapes must be \uXXXX or \UXXXXXXXX.`
	return []struct{ source, code, message, position, hint string }{
		{`E'\0')`, "22021", `invalid byte sequence for encoding "UTF8": 0x00`, "", ""},
		{`E'\000')`, "22021", `invalid byte sequence for encoding "UTF8": 0x00`, "", ""},
		{`E'\400')`, "22021", `invalid byte sequence for encoding "UTF8": 0x00`, "", ""},
		{`E'\x00')`, "22021", `invalid byte sequence for encoding "UTF8": 0x00`, "", ""},
		{`E'\777')`, "22021", `invalid byte sequence for encoding "UTF8": 0xff`, "", ""},
		{`E'\xff')`, "22021", `invalid byte sequence for encoding "UTF8": 0xff`, "", ""},
		{`E'\x80')`, "22021", `invalid byte sequence for encoding "UTF8": 0x80`, "", ""},
		{`E'\xc3')`, "22021", `invalid byte sequence for encoding "UTF8": 0xc3`, "", ""},
		{`E'ok\xc3x')`, "22021", `invalid byte sequence for encoding "UTF8": 0xc3 0x78`, "", ""},
		{`E'\xc0\xaf')`, "22021", `invalid byte sequence for encoding "UTF8": 0xc0 0xaf`, "", ""},
		{`E'\xed\xa0\x80')`, "22021", `invalid byte sequence for encoding "UTF8": 0xed 0xa0 0x80`, "", ""},
		{`E'\xf4\x90\x80\x80')`, "22021", `invalid byte sequence for encoding "UTF8": 0xf4 0x90 0x80 0x80`, "", ""},
		{`E'\u061')`, "22025", "invalid Unicode escape", "25", h},
		{`E'\U0061')`, "22025", "invalid Unicode escape", "25", h},
		{`E'\u0000')`, "42601", `invalid Unicode escape value at or near "\u0000"`, "25", ""},
		{`E'\U00000000')`, "42601", `invalid Unicode escape value at or near "\U00000000"`, "25", ""},
		{`E'\U00110000')`, "42601", `invalid Unicode escape value at or near "\U00110000"`, "25", ""},
		{`E'\uDC00')`, "42601", `invalid Unicode surrogate pair at or near "\uDC00"`, "25", ""},
		{`E'\uD800\u0061')`, "42601", `invalid Unicode surrogate pair at or near "\u0061"`, "31", ""},
		{`E'\uD800\u0000')`, "42601", `invalid Unicode surrogate pair at or near "\u0000"`, "31", ""},
		{`E'\uD800\U00110000')`, "42601", `invalid Unicode surrogate pair at or near "\U00110000"`, "31", ""},
		{`E'\uD800\u061')`, "22025", "invalid Unicode escape", "31", h},
		{`E'\uD800x')`, "42601", `invalid Unicode surrogate pair at or near "x"`, "31", ""},
		{`E'\uD800é')`, "42601", "invalid Unicode surrogate pair at or near \"\xc3\"", "31", ""},
		{`E'\uD800😀')`, "42601", "invalid Unicode surrogate pair at or near \"\xf0\"", "31", ""},
		{`E'\uD800')`, "42601", `invalid Unicode surrogate pair at or near "'"`, "31", ""},
		{`E'\uD800`, "42601", "invalid Unicode surrogate pair at end of input", "31", ""},
		{`E'abc`, "42601", `unterminated quoted string at or near "E'abc"`, "23", ""},
		{`E'abc\`, "42601", `unterminated quoted string at or near "E'abc\"`, "23", ""},
		{`E'abc\'`, "42601", `unterminated quoted string at or near "E'abc\'"`, "23", ""},
		{"E'a'\n'b", "42601", "unterminated quoted string at or near \"E'a'\n'b\"", "23", ""},
		{"E'\\uD800\n')", "42601", "invalid Unicode surrogate pair at or near \"\n\"", "31", ""},
		{"E'\\uD800'\n'\\uDC00')", "42601", `invalid Unicode surrogate pair at or near "'"`, "31", ""},
		{"E'a'\n'\\xff')", "22021", `invalid byte sequence for encoding "UTF8": 0xff`, "", ""},
		{"$$a)", "42601", `unterminated dollar-quoted string at or near "$$a)"`, "23", ""},
		{"$Tag$a$tag$)", "42601", `unterminated dollar-quoted string at or near "$Tag$a$tag$)"`, "23", ""},
		{"$tag$first\nsecond)", "42601", "unterminated dollar-quoted string at or near \"$tag$first\nsecond)\"", "23", ""},
		{"$)", "42601", `syntax error at or near "$"`, "23", ""},
		{"$tag)", "42601", `syntax error at or near "$"`, "23", ""},
		{"$_9)", "42601", `syntax error at or near "$"`, "23", ""},
		{"$tag-name$)", "42601", `syntax error at or near "$"`, "23", ""},
		{"$", "42601", `syntax error at or near "$"`, "23", ""},
		{"$tag", "42601", `syntax error at or near "$"`, "23", ""},
	}
}
