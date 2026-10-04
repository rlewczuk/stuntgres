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
	if len(create.Name.Parts) != 1 || create.Name.Parts[0].Text != "Mixed;Name" || create.Columns[0].Name != "integer" || create.Columns[0].Nullable || create.Columns[1].Name != "a\"b" || !create.Columns[1].Nullable || create.Columns[3].Type != Int8 {
		t.Fatalf("create = %#v", create)
	}
	insert := statements[1].(*Insert)
	want := [][]Value{
		{{Kind: IntegerValue, Integer: 12}, {Kind: StringValue, Text: "O'Brien; $99 \\\\ stays"}, {Kind: BooleanValue, Boolean: true}, {Kind: IntegerValue, Integer: math.MinInt64}},
		{{Kind: NullValue}, {Kind: StringValue, Text: "firstsecond"}, {Kind: BooleanValue}, {Kind: IntegerValue, Integer: math.MaxInt64}},
	}
	for i := range insert.Rows {
		for j := range insert.Rows[i] {
			got := insert.Rows[i][j]
			got.Pos = Position{}
			if !reflect.DeepEqual(got, want[i][j]) {
				t.Errorf("row %d value %d: %#v != %#v", i, j, got, want[i][j])
			}
		}
	}
	selectStmt := statements[2].(*Select)
	if !reflect.DeepEqual(selectStmt.Columns, []string{"integer", "a\"b"}) || selectStmt.Where.Value.Parameter != 2 || selectStmt.Order.Column != "active" || !selectStmt.Order.Desc {
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
	if got.Name.Parts[0].Text != strings.Repeat("a", 62) || got.Columns[0].Name != strings.Repeat("A", 62) || got.Columns[1].Name != "École" || got.Columns[2].Name != "bigint" || got.Columns[3].Name != "values" {
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
	if len(selectStmt.Name.Parts) != 1 || selectStmt.Name.Parts[0].Text != "public.t" {
		t.Fatalf("select name = %#v", selectStmt.Name)
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
		{"CREATE TABLE t ()", "42601"}, {"CREATE TABLE t (x INT", "42601"},
		{"SELECT FROM t", "42601"}, {"SELECT * t", "42601"},
		{"SELECT * FROM t; nonsense", "42601"}, {"INSERT INTO t VALUES ('a' 'b')", "42601"},
		{"INSERT INTO t VALUES ('a' /*\n*/ 'b')", "42601"},
		{"INSERT INTO t VALUES ($0)", "42P02"}, {"INSERT INTO t VALUES ($000)", "42P02"},
		{"INSERT INTO t VALUES ($2147483648)", "42601"}, {"INSERT INTO t VALUES ($1junk)", "42601"},
		{"INSERT INTO t VALUES (?)", "42601"}, {"INSERT INTO t VALUES (:name)", "42601"},
		{"INSERT INTO t VALUES (9223372036854775808)", "22003"},
		{"INSERT INTO t VALUES (-9223372036854775809)", "22003"},
		{"INSERT INTO t VALUES (1.0)", "0A000"}, {"INSERT INTO t VALUES (.1)", "0A000"},
		{"INSERT INTO t VALUES (1e3)", "0A000"}, {"INSERT INTO t VALUES (1z)", "42601"},
		{"INSERT INTO t VALUES (B'a')", "0A000"}, {"INSERT INTO t VALUES (X'a')", "0A000"},
		{"INSERT INTO t VALUES (N'a')", "0A000"}, {"INSERT INTO t VALUES ($tag$a$tag$)", "0A000"},
		{"INSERT INTO t VALUES (U&'a')", "0A000"}, {"CREATE TABLE U&\"t\" (x INT)", "0A000"},
		{"CREATE TABLE t (x TIMESTAMP)", "0A000"}, {"CREATE TABLE t (x NUMERIC(8))", "0A000"},
		{"CREATE TABLE t (x INT PRIMARY KEY)", "0A000"},
		{"SELECT * FROM t LIMIT 1", "0A000"}, {"INSERT INTO t VALUES (1) RETURNING x", "0A000"},
		{"SELECT * FROM t alias", "0A000"}, {"SELECT x alias FROM t", "0A000"},
		{"SELECT x FROM t ORDER BY x, y", "0A000"}, {"SELECT f(x) FROM t", "0A000"},
		{"SELECT x + 1 FROM t", "0A000"}, {"SELECT 1 FROM t", "0A000"},
		{"CREATE TABLE IF NOT EXISTS t (x INT)", "0A000"}, {"CREATE TABLE t (x INT[])", "0A000"},
		{"SELECT * FROM t JOIN u ON t.x = u.x", "0A000"}, {"SELECT * FROM t WHERE x = 1::INT", "0A000"},
		{"SELECT * FROM a.b.c", "0A000"}, {"SELECT * FROM public.", "42601"},
		{"UPDATE t SET x = 1", "0A000"}, {"DELETE FROM t", "0A000"}, {"BEGIN", "0A000"},
		{"START TRANSACTION", "0A000"}, {"COMMIT", "0A000"}, {"ROLLBACK", "0A000"},
		{"SAVEPOINT x", "0A000"}, {"RELEASE x", "0A000"},
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

func TestDiagnosticPositions(t *testing.T) {
	for _, tc := range []struct {
		query, message, position string
	}{
		{"INSERT INTO t VALUES ('é', )", `syntax error at or near ")"`, "28"},
		{"INSERT INTO t VALUES ('a', )", `syntax error at or near ")"`, "28"},
		{"INSERT INTO t VALUES ('😀', )", `syntax error at or near ")"`, "28"},
		{"CREATE TABLE t (x INT", "syntax error at end of input", "22"},
		{"CREATE TABLE t ()", `syntax error at or near ")"`, "17"},
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
	if values[0].Kind != StringValue || values[0].Text != "$17" || values[1].Parameter != 2 || values[2].Parameter != 2 || values[3].Parameter != 1 {
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
			v := stmts[0].(*Insert).Rows[0][0]
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
