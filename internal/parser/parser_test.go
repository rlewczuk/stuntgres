package parser

import (
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestScriptAndLiterals(t *testing.T) {
	script := `; /* outer /* nested */ comment */
 CREATE TABLE "Mixed;Name" (INTEGER INT NOT NULL, "a""b" TEXT NULL, active BOOL, wide INT8);
 INSERT INTO "Mixed;Name" (INTEGER, "a""b", active, wide) VALUES
 (+12, 'O''Brien; $99 \\ stays', TRUE, -9223372036854775808),
 (NULL, 'first' -- continue a standard string
 'second', FALSE, 9223372036854775807);
 SELECT INTEGER, "a""b" FROM "Mixed;Name" WHERE INTEGER = $2 ORDER BY active DESC;; -- end`
	statements, err := Parse(script)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 3 {
		t.Fatalf("statements = %d", len(statements))
	}
	create := statements[0].(*CreateTable)
	if len(create.Name.Parts) != 1 || create.Name.Parts[0].Text != "Mixed;Name" || create.Elements[0].Column.Name != "integer" || len(create.Elements[0].Column.Constraints) != 1 || !create.Elements[0].Column.Constraints[0].NotNull || create.Elements[1].Column.Name != "a\"b" || len(create.Elements[1].Column.Constraints) != 1 || create.Elements[1].Column.Constraints[0].NotNull || create.Elements[3].Column.TypeName.Name != "bigint" {
		t.Fatalf("create = %#v", create)
	}
	insert := statements[1].(*Insert)
	want := [][]Value{
		{{Kind: IntegerValue, Integer: 12}, {Kind: StringValue, Text: "O'Brien; $99 \\\\ stays"}, {Kind: BooleanValue, Boolean: true}, {Kind: IntegerValue, Integer: math.MinInt64}},
		{{Kind: NullValue}, {Kind: StringValue, Text: "firstsecond"}, {Kind: BooleanValue}, {Kind: IntegerValue, Integer: math.MaxInt64}},
	}
	for i := range insert.Rows {
		for j := range insert.Rows[i] {
			expr := insert.Rows[i][j]
			if unary, ok := expr.(OperatorExpr); ok {
				expr = unary.Args[0]
			}
			got := expr.(LiteralExpr).Value
			got.Pos = Position{}
			if !reflect.DeepEqual(got, want[i][j]) {
				t.Errorf("row %d value %d: %#v != %#v", i, j, got, want[i][j])
			}
		}
	}
	selectStmt := statements[2].(*Select)
	if selectStmt.Targets[0].(ColumnExpr).Name.Parts[0].Text != "integer" || selectStmt.Targets[1].(ColumnExpr).Name.Parts[0].Text != "a\"b" || selectStmt.Where.(OperatorExpr).Args[1].(ParameterExpr).Number != 2 || selectStmt.Order.Keys[0].Expr.(ColumnExpr).Name.Parts[0].Text != "active" || !selectStmt.Order.Keys[0].Desc {
		t.Fatalf("select = %#v", selectStmt)
	}
}

func TestIdentifierNormalization(t *testing.T) {
	long := strings.Repeat("A", 62) + "éZ"
	statements, err := Parse(`CREATE TABLE ` + long + ` ("` + long + `" TEXT, ÉCOLE INT, bigint BOOL, values TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	got := statements[0].(*CreateTable)
	if got.Name.Parts[0].Text != strings.Repeat("a", 62) || got.Elements[0].Column.Name != strings.Repeat("A", 62) || got.Elements[1].Column.Name != "École" || got.Elements[2].Column.Name != "bigint" || got.Elements[3].Column.Name != "values" {
		t.Fatalf("normalized = %#v", got)
	}
}

func TestQualifiedRelationComponents(t *testing.T) {
	statements, err := Parse(`CREATE TABLE public."a.b" (id INT); SELECT * FROM "public.t";`)
	if err != nil {
		t.Fatal(err)
	}
	create := statements[0].(*CreateTable)
	selectStmt := statements[1].(*Select)
	if len(create.Name.Parts) != 2 || create.Name.Parts[0].Text != "public" || create.Name.Parts[1].Text != "a.b" || create.Name.Parts[0].Pos.Offset != 13 || create.Name.Parts[1].Pos.Offset != 20 {
		t.Fatalf("create name = %#v", create.Name)
	}
	if name := selectStmt.From.(*TableReference).Name; len(name.Parts) != 1 || name.Parts[0].Text != "public.t" {
		t.Fatalf("select name = %#v", name)
	}
}

func TestEmptyScripts(t *testing.T) {
	for _, query := range []string{"", " \n\t", "-- comment", "/* nested /* x */ */", ";;; -- trailing"} {
		got, err := Parse(query)
		if err != nil || len(got) != 0 {
			t.Errorf("Parse(%q) = %#v, %v", query, got, err)
		}
	}
}

func TestParserErrorBoundaries(t *testing.T) {
	cases := []struct{ query, code string }{
		{"CREATE TABLE t (x INT", "42601"},
		{"SELECT FROM )", "42601"}, {"SELECT * t", "42601"},
		{"SELECT * FROM t; nonsense", "42601"}, {"INSERT INTO t VALUES ('a' 'b')", "42601"},
		{"INSERT INTO t VALUES ('a' /*\n*/ 'b')", "42601"},
		{"INSERT INTO t VALUES ($0)", "42P02"}, {"INSERT INTO t VALUES ($000)", "42P02"},
		{"INSERT INTO t VALUES ($2147483648)", "42601"}, {"INSERT INTO t VALUES ($1junk)", "42601"},
		{"INSERT INTO t VALUES (?)", "42601"}, {"INSERT INTO t VALUES (:name)", "42601"},
		{"INSERT INTO t VALUES (1z)", "42601"},
		{"INSERT INTO t VALUES (N'a')", "0A000"},
		{"CREATE TABLE t (x INT PRIMARY KEY)", "0A000"},
		{"INSERT INTO t VALUES (1) RETURNING x", "0A000"},
		{"SELECT f(x) FROM t", "0A000"},
		{"SELECT * FROM a.b.c", "0A000"}, {"SELECT * FROM public.", "42601"},
		{"DELETE FROM t USING", "42601"},
		{"CREATE TABLE \"\" (x INT)", "42601"}, {"SELECT * FROM \"unterminated", "42601"},
		{"INSERT INTO t VALUES ('unterminated)", "42601"}, {"/* unterminated", "42601"},
		{"SELECT * FROM t\x00", "42601"}, {"INSERT INTO t VALUES ('\x00')", "42601"},
		{"SELECT * FROM \xff", "42601"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			got, err := Parse(tc.query)
			var sqlErr *sqlstate.Error
			if !errors.As(err, &sqlErr) || sqlErr.Code != tc.code {
				t.Fatalf("Parse = %#v, %v; want SQLSTATE %s", got, err, tc.code)
			}
			if got != nil {
				t.Fatalf("partial script returned: %#v", got)
			}
		})
	}
}

func TestDollarStrings(t *testing.T) {
	tag := "$" + strings.Repeat("a", 64) + "$"
	for _, tc := range []struct{ source, text string }{
		{`$$a;'b$$`, "a;'b"}, {`$$$$`, ""}, {`$tag$$tag$`, ""},
		{`$tag$a$TAG$b$tag$`, "a$TAG$b"}, {`$tag$a$junk$tag$`, "a$junk"},
		{`$_9$é😀$_9$`, "é😀"}, {`$é$unicode$é$`, "unicode"},
		{"$T$first\nsecond; $1 \\tail$T$", "first\nsecond; $1 \\tail"},
		{tag + "long" + tag, "long"},
		{tag + "a$" + strings.Repeat("a", 63) + "b$" + tag, "a$" + strings.Repeat("a", 63) + "b$"},
	} {
		for _, prefix := range []string{"", "/*é*/ "} {
			q := prefix + "INSERT INTO t VALUES (" + tc.source + "); SELECT * FROM t"
			statements, err := Parse(q)
			if err != nil || len(statements) != 2 {
				t.Fatalf("%q: %#v %v", q, statements, err)
			}
			value := statements[0].(*Insert).Rows[0][0].(LiteralExpr).Value
			wantPos := Position{Offset: 22, Character: 23}
			if prefix != "" {
				wantPos = Position{Offset: 29, Character: 29}
			}
			if value.Kind != StringValue || value.Text != tc.text || value.Pos != wantPos {
				t.Fatalf("value %#v, want %q at %#v", value, tc.text, wantPos)
			}
			l := lexer{input: tc.source}
			var sym sqlSymType
			if l.Lex(&sym) != STRING || sym.tok.raw != tc.source || sym.tok.text != tc.text {
				t.Fatalf("raw token %#v", sym.tok)
			}
		}
	}
	for _, tc := range []struct{ source, message string }{
		{"$$a)", `unterminated dollar-quoted string at or near "$$a)"`},
		{"$Tag$a$tag$)", `unterminated dollar-quoted string at or near "$Tag$a$tag$)"`},
		{"$tag$first\nsecond)", "unterminated dollar-quoted string at or near \"$tag$first\nsecond)\""},
		{"$", `syntax error at or near "$"`}, {"$tag", `syntax error at or near "$"`},
		{"$_9)", `syntax error at or near "$"`}, {"$tag-name$)", `syntax error at or near "$"`},
	} {
		for _, prefix := range []string{"", "/*é*/ ", "/*😀*/ "} {
			statements, err := Parse(prefix + "INSERT INTO t VALUES (" + tc.source)
			position := "23"
			if prefix != "" {
				position = "29"
			}
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: position}
			var got *sqlstate.Error
			if statements != nil || !errors.As(err, &got) || *got != want || got.SQLState() != "42601" {
				t.Fatalf("diagnostic %#v %v, want %#v", got, err, want)
			}
		}
	}
	l := lexer{input: "VALUES$$v$$ x$tag$x$tag$"}
	var sym sqlSymType
	for _, want := range []string{"VALUES$$v$$", "x$tag$x$tag$"} {
		if l.Lex(&sym) != IDENT || sym.tok.raw != want {
			t.Fatalf("identifier %#v", sym.tok)
		}
	}
}

func TestDiagnosticPositions(t *testing.T) {
	for _, tc := range []struct {
		query, message, position string
	}{
		{"INSERT INTO t VALUES ('é', )", `syntax error at or near ")"`, "28"},
		{"INSERT INTO t VALUES ('a', )", `syntax error at or near ")"`, "28"},
		{"INSERT INTO t VALUES ('😀', )", `syntax error at or near ")"`, "28"},
		{"CREATE TABLE t (x INT", "syntax error at end of input", "22"},
		{"/*é*/ /* never closed", `unterminated /* comment at or near "/* never closed"`, "7"},
		{"INSERT INTO t VALUES ('é", `unterminated quoted string at or near "'é"`, "23"},
		{`SELECT * FROM "Bad`, `unterminated quoted identifier at or near ""Bad"`, "15"},
		{"SELECT * FROM T WHERE X = )", `syntax error at or near ")"`, "27"},
		{`SELECT * FROM t WHERE x "Quoted"`, `syntax error at or near ""Quoted""`, "25"},
		{"SELECT * FROM T WHERE X TRUE", `syntax error at or near "TRUE"`, "25"},
		{"INSERT INTO t VALUES ('first'\n 'later", "unterminated quoted string at or near \"'first'\n 'later\"", "23"},
		{`SELECT * FROM "" trailing`, `zero-length delimited identifier at or near """"`, "15"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			_, err := Parse(tc.query)
			var got *sqlstate.Error
			if !errors.As(err, &got) || got.Code != "42601" || got.Message != tc.message ||
				got.Position != tc.position || got.Severity != "ERROR" || got.SeverityUnlocalized != "ERROR" {
				t.Fatalf("diagnostic = %#v, %v; want %s at %s", got, err, tc.message, tc.position)
			}
			if got.Detail != "" || got.Hint != "" || got.InternalPosition != "" ||
				got.InternalQuery != "" || got.Context != "" || got.Schema != "" ||
				got.Table != "" || got.Column != "" || got.DataType != "" || got.Constraint != "" {
				t.Fatalf("unexpected optional fields: %#v", got)
			}
		})
	}
}

func TestInvalidInputCharacterPositions(t *testing.T) {
	for _, tc := range []struct {
		query, message, position string
	}{
		{"SELECT \xff", "invalid UTF-8 in SQL", "8"},
		{"SELECT 'é'\x00", "NUL in SQL", "11"},
		{"\x00SELECT \xff", "NUL in SQL", "1"},
		{"SELECT \xff\x00", "invalid UTF-8 in SQL", "8"},
	} {
		_, err := Parse(tc.query)
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "42601" || got.Message != tc.message ||
			got.Position != tc.position || got.Severity != "ERROR" || got.SeverityUnlocalized != "ERROR" {
			t.Errorf("Parse(%q) = %#v (%v), want %q at %s", tc.query, got, err, tc.message, tc.position)
		}
	}
}

func TestMarkersAndRepeatedParameters(t *testing.T) {
	got, err := Parse(`INSERT INTO t VALUES ('$17', $2, $2, $0001) /* $9 */`)
	if err != nil {
		t.Fatal(err)
	}
	values := got[0].(*Insert).Rows[0]
	if values[0].(LiteralExpr).Value.Kind != StringValue || values[0].(LiteralExpr).Value.Text != "$17" || values[1].(ParameterExpr).Number != 2 || values[2].(ParameterExpr).Number != 2 || values[3].(ParameterExpr).Number != 1 {
		t.Fatalf("values = %#v", values)
	}
}

func TestEscapeStringDecoding(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{`E''`, ""}, {`e'A'`, "A"}, {`E'\b\f\n\r\t\v'`, "\b\f\n\r\t\v"},
		{`E'O\'Brien\\tail'`, "O'Brien\\tail"}, {`E'O''Brien'`, "O'Brien"},
		{`E'\q\8\9\x\X'`, "q89xX"}, {"E'\\\n\\\r'", "\n\r"},
		{`E'\101\12\1\1012'`, "A\n\x01A2"}, {`E'\x41\x4\x414'`, "A\x04A4"},
		{`E'\303\251'`, "é"}, {`E'\xC3\xA9'`, "é"},
		{`E'\u0061\U00000062'`, "ab"}, {`E'\u00612'`, "a2"},
		{`E'\U0001F600'`, "😀"}, {`E'\uD83D\uDE00'`, "😀"},
		{`E'\uD83D\U0000DE00'`, "😀"}, {`E'\U0000D83D\uDE00'`, "😀"},
		{`E'\uFFFF\uFFFD'`, "\uFFFF\uFFFD"}, {`'\n'`, `\n`},
		{`'O''Brien'`, "O'Brien"}, {"E'a'\n'\\n'", "a\n"},
		{"E'a'\r'\\n'", "a\n"}, {"'a'\n'\\n'", `a\n`},
		{"E'\\xC3'\n'\\xA9'", "é"}, {"E'a' -- comment\r'\\n'", "a\n"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			stmts, err := Parse("/*é*/ INSERT INTO t VALUES (" + tc.source + ")")
			if err != nil {
				t.Fatal(err)
			}
			v := stmts[0].(*Insert).Rows[0][0].(LiteralExpr).Value
			if v.Kind != StringValue || v.Text != tc.want || v.Pos != (Position{Offset: 29, Character: 29}) {
				t.Fatalf("value = %#v, want %q at byte 29/character 29", v, tc.want)
			}
		})
	}
	if _, err := Parse("CREATE TABLE e (e TEXT)"); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`E 'a'`, `E'a' 'b'`, "E'a' /*\n*/ 'b'", "E'a'\nE'b'"} {
		if stmts, err := Parse("INSERT INTO t VALUES (" + source + ")"); err == nil || stmts != nil {
			t.Fatalf("accepted noncontinuation %q: %#v, %v", source, stmts, err)
		}
	}
}

func TestEscapeStringDiagnostics(t *testing.T) {
	const hint = `Unicode escapes must be \uXXXX or \UXXXXXXXX.`
	for _, tc := range []struct{ source, code, message, position, hint string }{
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
		{`E'\u061')`, "22025", "invalid Unicode escape", "25", hint},
		{`E'\U0061')`, "22025", "invalid Unicode escape", "25", hint},
		{`E'\u0000')`, "42601", `invalid Unicode escape value at or near "\u0000"`, "25", ""},
		{`E'\U00000000')`, "42601", `invalid Unicode escape value at or near "\U00000000"`, "25", ""},
		{`E'\U00110000')`, "42601", `invalid Unicode escape value at or near "\U00110000"`, "25", ""},
		{`E'\uDC00')`, "42601", `invalid Unicode surrogate pair at or near "\uDC00"`, "25", ""},
		{`E'\uD800\u0061')`, "42601", `invalid Unicode surrogate pair at or near "\u0061"`, "31", ""},
		{`E'\uD800\u0000')`, "42601", `invalid Unicode surrogate pair at or near "\u0000"`, "31", ""},
		{`E'\uD800\U00110000')`, "42601", `invalid Unicode surrogate pair at or near "\U00110000"`, "31", ""},
		{`E'\uD800\u061')`, "22025", "invalid Unicode escape", "31", hint},
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
	} {
		for _, prefix := range []string{"", "/*é*/ "} {
			query := prefix + "INSERT INTO t VALUES (" + tc.source
			position := tc.position
			if position != "" && prefix != "" {
				n, _ := strconv.Atoi(position)
				position = strconv.Itoa(n + 6)
			}
			stmts, err := Parse(query)
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: position, Hint: tc.hint}
			var got *sqlstate.Error
			if stmts != nil || !errors.As(err, &got) || *got != want || got.SQLState() != tc.code {
				t.Errorf("%q: %#v, %v; want %#v", query, got, err, want)
			}
			stmts, err = Parse("CREATE TABLE prior (x INT); " + query)
			if err == nil || stmts != nil {
				t.Errorf("partial script escaped rejection: %#v, %v", stmts, err)
			}
		}
	}
}

func TestNumericLiteralDecoding(t *testing.T) {
	cases := []struct {
		literal string
		want    int64
	}{
		{"0b1010", 10}, {"0B_1_010", 10}, {"0o12", 10}, {"0O_1_2", 10}, {"0xa", 10}, {"0X_A", 10},
		{"1_000", 1000}, {"001_000", 1000}, {"010", 10}, {"08", 8}, {"0x1EEE_FFFF", 518979583},
		{"0o2_73", 187}, {"0b_10_0101", 37}, {"0x1e3", 483},
		{"0x7FFF_FFFF", math.MaxInt32}, {"-0x8000_0000", math.MinInt32}, {"0x8000_0000", 2147483648},
		{"9_223_372_036_854_775_807", math.MaxInt64}, {"-9_223_372_036_854_775_808", math.MinInt64},
		{"0x7FFF_FFFF_FFFF_FFFF", math.MaxInt64}, {"-0x8000_0000_0000_0000", math.MinInt64},
		{"0o777777777777777777777", math.MaxInt64}, {"-0o1000000000000000000000", math.MinInt64},
		{"0b" + strings.Repeat("1", 63), math.MaxInt64}, {"-0b1" + strings.Repeat("0", 63), math.MinInt64},
		{"+0x7FFF_FFFF_FFFF_FFFF", math.MaxInt64}, {"- /*comment*/ 0x8000_0000_0000_0000", math.MinInt64},
	}
	for _, base := range []string{"", "0b", "0o", "0x"} {
		for _, sign := range []string{"", "+", "-"} {
			cases = append(cases, struct {
				literal string
				want    int64
			}{sign + base + "0", 0})
			want := int64(1)
			if sign == "-" {
				want = -1
			}
			cases = append(cases, struct {
				literal string
				want    int64
			}{sign + base + "1", want})
		}
	}
	for _, c := range cases {
		t.Run(c.literal, func(t *testing.T) {
			statements, err := Parse("INSERT INTO t VALUES (" + c.literal + ")")
			if err != nil {
				t.Fatal(err)
			}
			expr := statements[0].(*Insert).Rows[0][0]
			pos := expr.SourcePosition()
			if unary, ok := expr.(OperatorExpr); ok {
				expr = unary.Args[0]
			}
			got := expr.(LiteralExpr).Value
			got.Pos = pos
			if got.Kind != IntegerValue || got.Integer != c.want || got.Pos.Offset != 22 || got.Pos.Character != 23 {
				t.Fatalf("value %#v, want %d at 23", got, c.want)
			}
		})
	}
	if _, err := Parse("CREATE TABLE _100(_1_000 INT); SELECT _1_000 FROM _100"); err != nil {
		t.Fatal(err)
	}
}

func TestNumericLiteralLexicalPrecedenceAndNativeLimits(t *testing.T) {
	check := func(query, code, message, position string) {
		t.Helper()
		statements, err := Parse(query)
		var got *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
		if statements != nil || !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
			t.Fatalf("Parse(%q) = %#v, %#v (%v), want %#v", query, statements, got, err, want)
		}
	}
	for _, c := range []struct{ tokens, reason string }{
		{"0b 0B 0b_", "invalid binary integer"}, {"0o 0O 0o_", "invalid octal integer"}, {"0x 0X 0x_", "invalid hexadecimal integer"},
		{"0b2 0b102 0o8 0o78 0xg 0x1g 0b__1 0o__1 0x__1 0b_2 0x_g 1_ 1__0 1_000_ 1_000__0 0x1_ 0x1__f 1z 1b 1o 1x 0b1junk 0o1$ 10é 1efoo 1e+ 1e3foo 1e3_ 1_000e3__0 1E3foo 1e+3foo 1e-3_ 1_000E+3__0", "trailing junk after numeric literal"},
	} {
		for _, token := range strings.Fields(c.tokens) {
			check("INSERT INTO t VALUES ("+token+")", "42601", c.reason+` at or near "`+token+`"`, "23")
			check("INSERT INTO t VALUES (1),("+token+")", "42601", c.reason+` at or near "`+token+`"`, "27")
		}
	}
	for _, token := range []string{"9223372036854775808", "0x8000000000000000", "0o1000000000000000000000", "0b1" + strings.Repeat("0", 63), strings.Repeat("9", 200)} {
		if _, err := Parse("INSERT INTO t VALUES (" + token + ")"); err != nil {
			t.Fatal("valid numeric literal rejected", err)
		}
		junk := token + "z"
		check("INSERT INTO t VALUES ("+junk+")", "42601", `trailing junk after numeric literal at or near "`+junk+`"`, "23")
	}
	for _, token := range []string{"9223372036854775809", "0x8000000000000001", "0o1000000000000000000001", "0b1" + strings.Repeat("0", 62) + "1"} {
		if _, err := Parse("INSERT INTO t VALUES (-" + token + ")"); err != nil {
			t.Fatal("valid signed numeric literal rejected", err)
		}
	}
	check("INSERT INTO t VALUES (-0b2)", "42601", `trailing junk after numeric literal at or near "0b2"`, "24")
	check("/*é*/ INSERT INTO t VALUES (0b2)", "42601", `trailing junk after numeric literal at or near "0b2"`, "29")
	check("/*😀*/ INSERT INTO t VALUES (0x_)", "42601", `invalid hexadecimal integer at or near "0x_"`, "29")
	check("INSERT INTO t VALUES (1); INSERT INTO t VALUES (0b2)", "42601", `trailing junk after numeric literal at or near "0b2"`, "49")
}

func TestNoticesCreateSyntax(t *testing.T) {
	for _, q := range []string{"CREATE TABLE IF NOT EXISTS public.t(id INT)", "CREATE TABLE if(exists INT)", "CREATE TABLE exists(if TEXT)"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		create, ok := statements[0].(*CreateTable)
		if !ok {
			t.Fatal(statements)
		}
		if create.IfNotExists != strings.Contains(q, "IF NOT EXISTS") {
			t.Fatal(create)
		}
	}
	statements, err := Parse("CREATE TABLE IF NOT EXISTS prefix(id INT) garbage")
	if err == nil || len(statements) != 0 {
		t.Fatal(statements, err)
	}
}

func TestUnicodeEscapesValuesAndPositions(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{`U&'\0041'`, "A"}, {`u&'\+01f600'`, "😀"}, {`U&'\D83D\DE00'`, "😀"},
		{`U&'\+00D83D\DE00'`, "😀"}, {`U&'\D83D\+00DE00'`, "😀"},
		{`U&'\+00D83D\+00DE00'`, "😀"}, {`U&'\D800\DC00'`, "\U00010000"},
		{`U&'\DBFF\DFFF'`, "\U0010FFFF"}, {`U&'\D7FF\E000\FFFF'`, "\uD7FF\uE000\uFFFF"},
		{`U&'\00410'`, "A0"}, {`U&'\+0000410'`, "A0"}, {`U&'O''Brien\\'`, "O'Brien\\"},
		{`U&'!0041!!\0041' UESCAPE E'!'`, `A!\0041`},
		{`U&'!0041' /*x*/ UESCAPE $$!$$`, "A"}, {`U&''`, ""},
		{"U&'\\00'\n'41'", "A"}, {"U&'\\D83D' --x\r'\\DE00'", "😀"},
	} {
		statements, err := Parse("/*é*/ INSERT INTO t VALUES (" + tc.source + ",'tail'); SELECT * FROM t ORDER BY x")
		if err != nil {
			t.Fatalf("%q: %v", tc.source, err)
		}
		v := statements[0].(*Insert).Rows[0][0].(LiteralExpr).Value
		if v.Text != tc.want || v.Kind != StringValue || v.Pos != (Position{Offset: 29, Character: 29}) {
			t.Fatalf("%q: %#v", tc.source, v)
		}
		if statements[0].(*Insert).Rows[0][1].(LiteralExpr).Value.Text != "tail" || len(statements) != 2 {
			t.Fatal("lookahead lost")
		}
	}
	for _, source := range []string{`U &'a'`, `U& 'a'`, `U&'a' 'b'`, "U&'a'\nU&'b'", "U&'a'\nE'b'", "U&'a' /*\n*/ 'b'"} {
		if stmts, err := Parse("INSERT INTO t VALUES (" + source + ")"); err == nil || stmts != nil {
			t.Fatalf("accepted %q", source)
		}
	}
}

func TestUnicodeEscapesNamesAndRawLookahead(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{`U&"\0041"`, "A"}, {`U&"select"`, "select"}, {`U&"a\002eb"`, "a.b"},
		{`U&"UESCAPE"`, "UESCAPE"}, {`U&"FIRST"`, "FIRST"}, {`U&"NULLS"`, "NULLS"},
		{`U&"!0042" UESCAPE '!'`, "B"},
		{`U&"a""b\0022c"`, `a"b"c`}, {`U&"\+01F600"`, "😀"},
		{`U&"` + strings.Repeat(`\0061`, 64) + `"`, strings.Repeat("a", 63)},
		{`U&"` + strings.Repeat("a", 62) + `\00e9"`, strings.Repeat("a", 62)},
		{`U&"` + strings.Repeat("a", 59) + `\+01f600"`, strings.Repeat("a", 59) + "😀"},
	} {
		s, err := Parse("CREATE TABLE " + tc.source + "(x TEXT)")
		if err != nil {
			t.Fatal(err)
		}
		id := s[0].(*CreateTable).Name.Parts[0]
		if id.Text != tc.want || id.Pos != (Position{Offset: 13, Character: 14}) {
			t.Fatalf("name %#v want %q", id, tc.want)
		}
	}
	// Inspect the raw/filtered boundary including cached EOF and quoted UESCAPE.
	l := &lexer{input: `U&'A' "UESCAPE" U&'B'`}
	var v sqlSymType
	for i, want := range []struct {
		code      int
		text, raw string
		character int
	}{
		{STRING, "A", `U&'A'`, 1}, {IDENT, "UESCAPE", `"UESCAPE"`, 7}, {STRING, "B", `U&'B'`, 17}, {0, "", "", 22}, {0, "", "", 22},
	} {
		code := l.Lex(&v)
		if code != want.code || v.tok.text != want.text || v.tok.raw != want.raw || v.tok.pos.Character != want.character || l.err != nil {
			t.Fatalf("token %d: %d %#v %v", i, code, v.tok, l.err)
		}
	}
}

func TestUnicodeEscapesDiagnostics(t *testing.T) {
	const h = `Unicode escapes must be \XXXX or \+XXXXXX.`
	for _, tc := range []struct{ query, message, position, hint string }{
		{`INSERT INTO t VALUES (U&'\061')`, "invalid Unicode escape", "26", h},
		{`INSERT INTO t VALUES (U&'\+0061')`, "invalid Unicode escape", "26", h},
		{`INSERT INTO t VALUES (U&'\0000')`, "invalid Unicode escape value", "26", ""},
		{`INSERT INTO t VALUES (U&'\+110000')`, "invalid Unicode escape value", "26", ""},
		{`INSERT INTO t VALUES (U&'\DC00')`, "invalid Unicode surrogate pair", "26", ""},
		{`INSERT INTO t VALUES (U&'\D800')`, "invalid Unicode surrogate pair", "31", ""},
		{`INSERT INTO t VALUES (U&'\D800\061')`, "invalid Unicode escape", "31", h},
		{`INSERT INTO t VALUES (U&'\D800\+110000')`, "invalid Unicode escape value", "31", ""},
		{`INSERT INTO t VALUES (U&'a''\061')`, "invalid Unicode escape", "28", h},
		{`INSERT INTO t VALUES (U&'a''😀\061')`, "invalid Unicode escape", "30", h},
		{"INSERT INTO t VALUES (U&'a'\n'😀x\\061')", "invalid Unicode escape", "31", h},
		{`SELECT * FROM U&"a""😀\061"`, "invalid Unicode escape", "22", h},
		{`CREATE TABLE fresh(x TEXT); INSERT INTO fresh VALUES (U&'a''😀\061')`, "invalid Unicode escape", "62", h},
		{"INSERT INTO t VALUES (U&'a'\n'\\061')", "invalid Unicode escape", "27", h},
		{`CREATE TABLE U&"a""\061"(x TEXT)`, "invalid Unicode escape", "19", h},
		{`INSERT INTO t VALUES (U&'\061' UESCAPE '+')`, `invalid Unicode escape character at or near "'+'"`, "40", ""},
		{`INSERT INTO t VALUES (U&'x' UESCAPE U&'\061')`, `UESCAPE must be followed by a simple string literal at or near "U&'\061'"`, "37", ""},
		{`INSERT INTO t VALUES (U&'\061' /*`, `unterminated /* comment at or near "/*"`, "32", ""},
		{`CREATE TABLE U&""(x TEXT)`, `zero-length delimited identifier at or near "U&"""`, "14", ""},
		{`CREATE TABLE U&"abc`, `unterminated quoted identifier at or near "U&"abc"`, "14", ""},
		{`CREATE TABLE fresh(x TEXT); INSERT INTO fresh VALUES (U&'\061')`, "invalid Unicode escape", "58", h},
	} {
		for _, prefix := range []string{"", "/*é*/ ", "/*😀*/ "} {
			n, _ := strconv.Atoi(tc.position)
			if prefix != "" {
				n += 6
			}
			s, err := Parse(prefix + tc.query)
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: strconv.Itoa(n), Hint: tc.hint}
			var got *sqlstate.Error
			if s != nil || !errors.As(err, &got) || *got != want {
				t.Fatalf("%q: %#v %v want %#v", prefix+tc.query, got, err, want)
			}
		}
	}
	// Every digit boundary is checked, including incomplete and nonhex escapes.
	for _, width := range []int{4, 6} {
		for i := range width {
			for _, tail := range []string{"", "G"} {
				escape := `\`
				if width == 6 {
					escape += "+"
				}
				q := "INSERT INTO t VALUES (U&'" + escape + strings.Repeat("0", i) + tail + "')"
				_, err := Parse(q)
				var got *sqlstate.Error
				if !errors.As(err, &got) || got.Code != "42601" || got.Message != "invalid Unicode escape" || got.Position != "26" || got.Hint != h {
					t.Fatalf("%q: %v", q, err)
				}
			}
		}
	}
}

func TestUnicodeEscapesDiagnosticByteLimits(t *testing.T) {
	// The emoji occupies bytes [1,5). Each interior byte limit counts its
	// complete source rune once; a limit at its start does not count it.
	for _, tc := range []struct {
		name     string
		limit    int
		position string
	}{
		{"rune start", 1, "2"},
		{"one byte overlap", 2, "3"},
		{"two byte overlap", 3, "3"},
		{"three byte overlap", 4, "3"},
		{"full rune boundary", 5, "3"},
		{"EOF", 6, "4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &lexer{input: "a😀z"}
			l.failDiagnostic("42601", "invalid Unicode escape", `Unicode escapes must be \XXXX or \+XXXXXX.`, tc.limit)
			want := sqlstate.Error{
				Severity: "ERROR", SeverityUnlocalized: "ERROR",
				Code: "42601", Message: "invalid Unicode escape",
				Position: tc.position, Hint: `Unicode escapes must be \XXXX or \+XXXXXX.`,
			}
			var got *sqlstate.Error
			if !errors.As(l.err, &got) || *got != want || got.SQLState() != want.Code {
				t.Fatalf("byte limit %d: %#v %v; want %#v", tc.limit, got, l.err, want)
			}
		})
	}
}

func TestUnicodeEscapesUESCAPERawValidation(t *testing.T) {
	forbidden := []string{"''", "'ab'", "'é'", "'😀'", "'+'", "''''", "'\"'", "' '", `E'\t'`, `E'\n'`, `E'\r'`, `E'\f'`, `E'\v'`}
	for _, c := range "0123456789abcdefABCDEF" {
		forbidden = append(forbidden, "'"+string(c)+"'")
	}
	for _, raw := range forbidden {
		q := "INSERT INTO t VALUES (U&'x' UESCAPE " + raw + ")"
		s, err := Parse(q)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid Unicode escape character at or near \"" + raw + "\"", Position: "37"}
		if s != nil || !errors.As(err, &got) || *got != want {
			t.Fatalf("%q: %#v %v want %#v", q, got, err, want)
		}
	}
	for _, raw := range []string{"$1", "1", `"!"`, `U&'\061'`, `U&"\061"`} {
		q := "INSERT INTO t VALUES (U&'x' UESCAPE " + raw + ")"
		s, err := Parse(q)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "UESCAPE must be followed by a simple string literal at or near \"" + raw + "\"", Position: "37"}
		if s != nil || !errors.As(err, &got) || *got != want {
			t.Fatalf("raw argument %q: %#v %v want %#v", raw, got, err, want)
		}
	}
	s, err := Parse("INSERT INTO t VALUES (U&'x' UESCAPE")
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "UESCAPE must be followed by a simple string literal at end of input", Position: "36"}
	if s != nil || !errors.As(err, &got) || *got != want {
		t.Fatalf("EOF: %#v %v want %#v", got, err, want)
	}
}

func TestUnicodeEscapesComposedRawCache(t *testing.T) {
	type expectedToken struct {
		code               int
		text, raw, keyword string
		character, scanned int
		cached             bool
	}
	for _, tc := range []struct {
		input  string
		tokens []expectedToken
	}{
		{`U&'A' NULLS FIRST`, []expectedToken{
			{STRING, "A", `U&'A'`, "", 1, 11, true},
			{NULLS_LA, "nulls", "NULLS", "nulls", 7, 17, true},
			{KW_U, "first", "FIRST", "first", 13, 17, false},
			{0, "", "", "", 18, 17, false}, {0, "", "", "", 18, 17, false},
		}},
		{`NULLS U&'A'`, []expectedToken{
			{KW_U, "nulls", "NULLS", "nulls", 1, 11, true},
			{STRING, "A", `U&'A'`, "", 7, 11, true},
			{0, "", "", "", 12, 11, false}, {0, "", "", "", 12, 11, false},
		}},
		{`U&"UESCAPE" U&"FIRST"`, []expectedToken{
			{IDENT, "UESCAPE", `U&"UESCAPE"`, "", 1, 21, true},
			{IDENT, "FIRST", `U&"FIRST"`, "", 13, 21, true},
			{0, "", "", "", 22, 21, false}, {0, "", "", "", 22, 21, false},
		}},
		{`U&'A' U&"UESCAPE"`, []expectedToken{
			{STRING, "A", `U&'A'`, "", 1, 17, true},
			{IDENT, "UESCAPE", `U&"UESCAPE"`, "", 7, 17, true},
			{0, "", "", "", 18, 17, false}, {0, "", "", "", 18, 17, false},
		}},
		{`NULLS U&"FIRST"`, []expectedToken{
			{KW_U, "nulls", "NULLS", "nulls", 1, 15, true},
			{IDENT, "FIRST", `U&"FIRST"`, "", 7, 15, true},
			{0, "", "", "", 16, 15, false}, {0, "", "", "", 16, 15, false},
		}},
		{`U&"NULLS" FIRST`, []expectedToken{
			{IDENT, "NULLS", `U&"NULLS"`, "", 1, 15, true},
			{KW_U, "first", "FIRST", "first", 11, 15, false},
			{0, "", "", "", 16, 15, false}, {0, "", "", "", 16, 15, false},
		}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			l := &lexer{input: tc.input}
			var v sqlSymType
			for i, want := range tc.tokens {
				code := l.Lex(&v)
				pos := Position{Offset: want.character - 1, Character: want.character}
				if l.err != nil || code != want.code || v.tok.text != want.text || v.tok.raw != want.raw || v.tok.pos != pos || l.current != v.tok || l.offset != want.scanned || l.hasLookahead != want.cached {
					t.Fatalf("token %d: %d %#v current %#v scanned %d cached %v %v; want %#v", i, code, v.tok, l.current, l.offset, l.hasLookahead, l.err, want)
				}
				if want.keyword == "" {
					if v.tok.keyword != nil {
						t.Fatalf("token %d acquired keyword %#v", i, v.tok.keyword)
					}
				} else if v.tok.keyword == nil || v.tok.keyword.word != want.keyword || v.tok.keyword.category != Unreserved {
					t.Fatalf("token %d lost unreserved keyword %q: %#v", i, want.keyword, v.tok.keyword)
				}
				if want.cached && i+1 < len(tc.tokens) {
					next := tc.tokens[i+1]
					if l.lookahead.lexeme.raw != next.raw || l.lookahead.lexeme.pos != (Position{Offset: next.character - 1, Character: next.character}) {
						t.Fatalf("token %d cached wrong source evidence: %#v", i, l.lookahead)
					}
				}
			}
		})
	}
	for _, tc := range []struct {
		input                                     string
		prefix                                    bool
		nullsCharacter, unicodeCharacter, scanned int
		position                                  string
	}{
		{`NULLS U&'\061'`, false, 1, 7, 14, "10"},
		{`U&'A' NULLS U&'\061'`, true, 7, 13, 20, "16"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			l := &lexer{input: tc.input}
			var v sqlSymType
			if tc.prefix {
				if code := l.Lex(&v); code != STRING || l.err != nil || l.current != v.tok || v.tok.text != "A" || v.tok.raw != `U&'A'` || v.tok.keyword != nil || v.tok.pos != (Position{Offset: 0, Character: 1}) || !l.hasLookahead || l.lookahead.lexeme.raw != "NULLS" || l.offset != 11 {
					t.Fatalf("Unicode did not cache raw NULLS: %#v %#v %v", v.tok, l.lookahead, l.err)
				}
			}
			if code := l.Lex(&v); code != KW_U || l.err != nil || l.current != v.tok || v.tok.text != "nulls" || v.tok.raw != "NULLS" || v.tok.pos != (Position{Offset: tc.nullsCharacter - 1, Character: tc.nullsCharacter}) || v.tok.keyword == nil || v.tok.keyword.word != "nulls" || v.tok.keyword.category != Unreserved || l.offset != tc.scanned {
				t.Fatalf("NULLS prematurely decoded raw lookahead: %#v %v", v.tok, l.err)
			}
			if !l.hasLookahead || !l.lookahead.unicode || l.lookahead.code != STRING || l.lookahead.lexeme.text != `\061` || l.lookahead.lexeme.raw != `U&'\061'` || l.lookahead.lexeme.keyword != nil || l.lookahead.lexeme.pos != (Position{Offset: tc.unicodeCharacter - 1, Character: tc.unicodeCharacter}) {
				t.Fatalf("malformed U token not retained raw: %#v", l.lookahead)
			}
			code := l.Lex(&v)
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid Unicode escape", Position: tc.position, Hint: `Unicode escapes must be \XXXX or \+XXXXXX.`}
			var got *sqlstate.Error
			if code != 0 || !errors.As(l.err, &got) || *got != want || got.SQLState() != want.Code {
				t.Fatalf("cached Unicode decode: code %d %#v %v; want %#v", code, got, l.err, want)
			}
		})
	}
}

func TestUnicodeEscapesCompositionDiagnostics(t *testing.T) {
	for _, tc := range []struct{ query, code, message, position, hint string }{
		{`SELECT x FROM t ORDER BY x NULLS U&"FIRST"`, "42601", `syntax error at or near "NULLS"`, "28", ""},
		{`INSERT INTO t SELECT x FROM source_rows; INSERT INTO t VALUES (U&'\061')`, "42601", "invalid Unicode escape", "67", `Unicode escapes must be \XXXX or \+XXXXXX.`},
	} {
		statements, err := Parse(tc.query)
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint}
		var got *sqlstate.Error
		if statements != nil || !errors.As(err, &got) || *got != want || got.SQLState() != want.Code {
			t.Fatalf("%q: %#v %v statements %#v; want %#v", tc.query, got, err, statements, want)
		}
	}
}

func TestUnicodeEscapesPendingQueryPrecedence(t *testing.T) {
	const query = `INSERT INTO t SELECT x FROM source_rows; INSERT INTO t VALUES (U&'\061')`
	l := &lexer{input: query}
	result := sqlParse(l)
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid Unicode escape", Position: "67", Hint: `Unicode escapes must be \XXXX or \+XXXXXX.`}
	var got *sqlstate.Error
	if result == 0 || !errors.As(l.err, &got) || *got != want || got.SQLState() != want.Code || len(l.statements) != 1 || l.statements[0].(*Insert).Source == nil {
		t.Fatalf("pending-query parse result %d, statements %#v, error %#v %v; want %#v", result, l.statements, got, l.err, want)
	}
	if l.pendingMessage != "" {
		t.Fatalf("supported query established a deferred capability: %q", l.pendingMessage)
	}
}

func TestExpressionCoreRawOccurrences(t *testing.T) {
	for _, q := range []string{"SELECT id,1,2147483648,'x',TRUE,FALSE,NULL,$1 FROM t WHERE f", "SELECT $1 FROM t WHERE id=$1", "SELECT id FROM t WHERE NULL", "SELECT id FROM t WHERE 'yes'"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		s := statements[0].(*Select)
		if len(s.Targets) == 0 || s.Where == nil {
			t.Fatal(s)
		}
		for _, raw := range s.Targets {
			if raw.SourcePosition().Offset < 0 || raw.SourcePosition().Character < 1 {
				t.Fatal(raw)
			}
		}
	}
	for _, q := range []string{"SELECT 1"} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 || statements[0].(*Select).From != nil {
			t.Fatal("no-FROM scalar syntax", q, statements, err)
		}
	}
}

func TestExpressionCoreNoFromFailurePrecedence(t *testing.T) {
	for _, tc := range []struct{ query, code string }{
		{"SELECT * t", "42601"},
		{"SELECT 1; )", "42601"},
		{"CREATE TABLE staged(id INT); SELECT 1; )", "42601"},
		{"SELECT 1; SELECT FROM )", "42601"},
	} {
		statements, err := Parse(tc.query)
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatalf("%s: statements=%#v diagnostic=%#v (%v), want %s", tc.query, statements, diagnostic, err, tc.code)
		}
	}
}

func TestFloatDeclarations(t *testing.T) {
	for _, tc := range []struct {
		sql, name string
		quoted    bool
	}{{"REAL", "real", false}, {"FLOAT", "float", false}, {"FLOAT(1)", "float4", false}, {"FLOAT(24)", "float4", false}, {"FLOAT(25)", "float8", false}, {"FLOAT(53)", "float8", false}, {"DOUBLE PRECISION", "double precision", false}, {`"float4"`, "float4", true}, {`"real"`, "real", true}} {
		stmts, err := Parse("CREATE TABLE f(x " + tc.sql + ")")
		if err != nil {
			t.Fatal(tc, err)
		}
		got := stmts[0].(*CreateTable).Elements[0].Column.TypeName
		if got.Name != tc.name || got.Quoted != tc.quoted {
			t.Fatal(tc, got)
		}
	}
	for _, tc := range []struct{ precision, code, message string }{{"0", "22023", "precision for type float must be at least 1 bit"}, {"54", "22023", "precision for type float must be less than 54 bits"}, {"-1", "42601", `syntax error at or near "-"`}, {"1.5", "42601", `syntax error at or near "1.5"`}, {"2147483648", "42601", `syntax error at or near "2147483648"`}} {
		_, err := Parse("CREATE TABLE f(x FLOAT(" + tc.precision + "))")
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: "24"}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%s: %#v want %#v", tc.precision, got, want)
		}
	}
	_, err := Parse("CREATE TABLE IF NOT EXISTS f(x FLOAT(0))")
	var got *sqlstate.Error
	if !errors.As(err, &got) || got.Code != "22023" {
		t.Fatal(err)
	}
}

func TestFloatPrecisionScannerPrecedence(t *testing.T) {
	for _, spelling := range []string{"1.5junk", ".5junk", "1.5e+", ".5e-", "1.5e", "1.5e+3junk", "1.5_", "1.5e+3_"} {
		_, err := Parse("CREATE TABLE f(x FLOAT(" + spelling + "))")
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `trailing junk after numeric literal at or near "` + spelling + `"`, Position: "24"}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%s: %#v want %#v", spelling, got, want)
		}
	}
	for _, spelling := range []string{".5", "1.5e+3"} {
		_, err := Parse("CREATE TABLE f(x FLOAT(" + spelling + "))")
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "` + spelling + `"`, Position: "24"}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(got, want)
		}
	}
	_, err := Parse("CREATE TABLE f(x FLOAT(1..5))")
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ".."`, Position: "25"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(got, want)
	}
	// FLOAT modifier restrictions must not leak into scalar numeric syntax.
	_, err = Parse("INSERT INTO f VALUES(1.5)")
	if err != nil {
		t.Fatal(err)
	}
}

func TestQueryCoreOrderOccurrencePosition(t *testing.T) {
	for _, tc := range []struct {
		query             string
		offset, character int
	}{
		{"SELECT id FROM t ORDER BY missing", 26, 27},
		{"SELECT /*é*/ id FROM t ORDER BY missing DESC", 33, 33},
	} {
		statements, err := Parse(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		order := statements[0].(*Select).Order
		if order == nil || order.Keys[0].Expr.(ColumnExpr).Name.Parts[0].Text != "missing" || order.Keys[0].Pos.Offset != tc.offset || order.Keys[0].Pos.Character != tc.character {
			t.Fatal(order, tc)
		}
	}
}
