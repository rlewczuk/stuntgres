package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestCopyParserBinaryQueryFrom(t *testing.T) {
	statements, err := Parse("COPY (SELECT 1) FROM STDIN WITH(FORMAT binary)")
	var record *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at or near \"FROM\"", Position: "17"}
	if statements != nil || !errors.As(err, &record) || *record != want {
		t.Fatalf("statements=%#v diagnostic=%#v err=%v", statements, record, err)
	}
}

func TestCopyParserLegacyBinary(t *testing.T) {
	position := func(q, token string) Position {
		t.Helper()
		offset := strings.Index(q, token)
		if offset < 0 {
			t.Fatalf("missing token %q in %q", token, q)
		}
		return Position{Offset: offset, Character: len([]rune(q[:offset])) + 1}
	}
	parseCopy := func(t *testing.T, q string) *Copy {
		t.Helper()
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatalf("Parse(%q): statements=%#v err=%v", q, statements, err)
		}
		copy, ok := statements[0].(*Copy)
		if !ok {
			t.Fatalf("Parse(%q): statement=%T, want *Copy", q, statements[0])
		}
		return copy
	}
	format := CopyOption{Name: "format", Value: "binary", HasValue: true, Argument: CopyArgumentScalar}
	modernSQL := `COPY "t" ( "id", "v" ) FROM STDIN WITH (FORMAT binary)`
	modern := parseCopy(t, modernSQL)
	modernWant := format
	modernWant.Pos = position(modernSQL, "FORMAT")
	if !reflect.DeepEqual(modern.Options, []CopyOption{modernWant}) {
		t.Fatalf("modern options=%#v want=%#v", modern.Options, modernWant)
	}
	for _, tc := range []struct {
		name     string
		sql      string
		from     bool
		endpoint CopyEndpoint
	}{
		{"pgx", `copy "t" ( "id", "v" ) from stdin binary;`, true, CopyStdin},
		{"uppercase", `COPY "t" ( "id", "v" ) FROM STDIN BINARY`, true, CopyStdin},
		{"with", `COPY "t" ( "id", "v" ) FROM STDIN WITH BiNaRy;`, true, CopyStdin},
		{"to", `COPY "t" ( "id", "v" ) TO STDOUT BINARY;`, false, CopyStdout},
		{"to_with", `COPY "t" ( "id", "v" ) TO STDOUT WITH BINARY;`, false, CopyStdout},
		{"multibyte_prefix", `/* é */ copy "t" ( "id", "v" ) from stdin binary;`, true, CopyStdin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := parseCopy(t, tc.sql)
			if copy.From != tc.from || copy.Endpoint != tc.endpoint || copy.Query != nil || copy.Filename != "" || copy.Where != nil {
				t.Fatalf("COPY target/direction/endpoint=%#v", copy)
			}
			if len(copy.Name.Parts) != 1 || copy.Name.Parts[0].Text != "t" ||
				len(copy.Columns) != 2 || copy.Columns[0].Text != "id" || copy.Columns[1].Text != "v" {
				t.Fatalf("COPY name=%#v columns=%#v", copy.Name, copy.Columns)
			}
			want := format
			// Locate the spelling in the input, independently of parser positions.
			offset := strings.Index(strings.ToLower(tc.sql), "binary")
			want.Pos = Position{Offset: offset, Character: len([]rune(tc.sql[:offset])) + 1}
			if !reflect.DeepEqual(copy.Options, []CopyOption{want}) {
				t.Fatalf("legacy options=%#v want=%#v", copy.Options, want)
			}
			legacyOption, modernOption := copy.Options[0], modern.Options[0]
			if legacyOption.Pos == modernOption.Pos {
				t.Fatal("legacy BINARY position replaced by modern FORMAT position")
			}
			legacyOption.Pos, modernOption.Pos = Position{}, Position{}
			if !reflect.DeepEqual(legacyOption, modernOption) {
				t.Fatalf("legacy=%#v modern=%#v", legacyOption, modernOption)
			}
		})
	}
	t.Run("composition_and_ownership", func(t *testing.T) {
		q := "/* é界 */ COPY \"té\" FROM STDIN HEADER BINARY NULL 'NIL' BINARY WHERE TRUE; COPY u TO STDOUT WITH BINARY HEADER"
		statements, err := Parse(q)
		if err != nil || len(statements) != 2 {
			t.Fatalf("statements=%#v err=%v", statements, err)
		}
		first, firstOK := statements[0].(*Copy)
		second, secondOK := statements[1].(*Copy)
		if !firstOK || !secondOK {
			t.Fatalf("statement types=%T,%T", statements[0], statements[1])
		}
		firstBinary, repeatedBinary, secondBinary := format, format, format
		firstBinary.Pos = position(q, "BINARY")
		repeatedBinary.Pos = position(q, "BINARY WHERE")
		secondBinary.Pos = position(q, "BINARY HEADER")
		firstWant := []CopyOption{
			{Name: "header", Pos: position(q, "HEADER")},
			firstBinary,
			{Name: "null", Value: "NIL", HasValue: true, Argument: CopyArgumentScalar, Pos: position(q, "NULL")},
			repeatedBinary,
		}
		secondWant := []CopyOption{
			secondBinary,
			{Name: "header", Pos: Position{Offset: strings.LastIndex(q, "HEADER"), Character: utf8.RuneCountInString(q[:strings.LastIndex(q, "HEADER")]) + 1}},
		}
		if !reflect.DeepEqual(first.Options, firstWant) || !reflect.DeepEqual(second.Options, secondWant) {
			t.Fatalf("composed options first=%#v second=%#v", first.Options, second.Options)
		}
		where, ok := first.Where.(LiteralExpr)
		if !ok || where.Value != (Value{Kind: BooleanValue, Boolean: true, Pos: position(q, "TRUE")}) ||
			!first.From || first.Endpoint != CopyStdin || second.From || second.Endpoint != CopyStdout || second.Where != nil {
			t.Fatalf("composed COPY first=%#v second=%#v", first, second)
		}
		// A later statement and a new parse must not overwrite retained semantic
		// values, and each result must own its option backing.
		later := parseCopy(t, "COPY t FROM STDIN BINARY")
		if first == second || first == later || &first.Options[0] == &second.Options[0] || &first.Options[0] == &later.Options[0] {
			t.Fatal("COPY statements share AST or option backing")
		}
		later.Options[0].Value = "text"
		if !reflect.DeepEqual(first.Options, firstWant) || !reflect.DeepEqual(second.Options, secondWant) {
			t.Fatalf("retained options changed first=%#v second=%#v", first.Options, second.Options)
		}
	})
	for _, q := range []string{
		"COPY t FROM STDIN unknown",
		"COPY t FROM STDIN WITH unknown",
		"COPY t FROM STDIN BINARY garbage",
		"COPY t FROM STDIN BINARY; garbage",
		"COPY t FROM STDIN BINARY; SELECT (",
		"COPY t FROM STDIN BINARY WHERE TRUE garbage",
	} {
		statements, err := Parse(q)
		var diagnostic *sqlstate.Error
		if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatalf("invalid whole COPY %q: statements=%#v diagnostic=%#v err=%v", q, statements, diagnostic, err)
		}
	}
}

func TestCopyParserLegacyBinarySupplement(t *testing.T) {
	parseCopy := func(t *testing.T, q string) *Copy {
		t.Helper()
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatalf("Parse(%q): statements=%#v err=%v", q, statements, err)
		}
		copy, ok := statements[0].(*Copy)
		if !ok {
			t.Fatalf("Parse(%q): statement=%T, want *Copy", q, statements[0])
		}
		return copy
	}
	position := func(q, token string) Position {
		t.Helper()
		offset := strings.Index(q, token)
		if offset < 0 {
			t.Fatalf("missing token %q in %q", token, q)
		}
		return Position{Offset: offset, Character: utf8.RuneCountInString(q[:offset]) + 1}
	}
	for _, tc := range []struct {
		name, sql    string
		parts        []string
		partTokens   []string
		columns      []string
		columnTokens []string
		binary       string
	}{
		{
			name: "no_column_list", sql: "COPY t FROM STDIN BiNaRy",
			parts: []string{"t"}, partTokens: []string{"t"}, binary: "BiNaRy",
		},
		{
			name: "no_column_list_with", sql: `COPY public."Legacy Rows" FROM STDIN WITH BINARY`,
			parts: []string{"public", "Legacy Rows"}, partTokens: []string{"public", `"Legacy Rows"`}, binary: "BINARY",
		},
		{
			name: "qualified_escaped_names", sql: `/* é界 */ COPY public."Ré""lation" ("v""alue", "é") FROM STDIN WITH BiNaRy`,
			parts: []string{"public", `Ré"lation`}, partTokens: []string{"public", `"Ré""lation"`},
			columns: []string{`v"alue`, "é"}, columnTokens: []string{`"v""alue"`, `"é"`}, binary: "BiNaRy",
		},
		{
			name: "quoted_schema", sql: `COPY "S""chema"."Legacy Rows" ("b", "a") FROM STDIN BINARY`,
			parts: []string{`S"chema`, "Legacy Rows"}, partTokens: []string{`"S""chema"`, `"Legacy Rows"`},
			columns: []string{"b", "a"}, columnTokens: []string{`"b"`, `"a"`}, binary: "BINARY",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := parseCopy(t, tc.sql)
			if !copy.From || copy.Endpoint != CopyStdin || copy.Query != nil || copy.Filename != "" || copy.Where != nil ||
				copy.Pos != position(tc.sql, "COPY") || len(copy.Name.Parts) != len(tc.parts) || len(copy.Columns) != len(tc.columns) {
				t.Fatalf("COPY=%#v", copy)
			}
			for i, text := range tc.parts {
				want := Identifier{Text: text, Pos: position(tc.sql, tc.partTokens[i])}
				if copy.Name.Parts[i] != want {
					t.Fatalf("name[%d]=%#v want=%#v", i, copy.Name.Parts[i], want)
				}
			}
			for i, text := range tc.columns {
				want := Identifier{Text: text, Pos: position(tc.sql, tc.columnTokens[i])}
				if copy.Columns[i] != want {
					t.Fatalf("column[%d]=%#v want=%#v", i, copy.Columns[i], want)
				}
			}
			want := []CopyOption{{Name: "format", Value: "binary", HasValue: true, Argument: CopyArgumentScalar, Pos: position(tc.sql, tc.binary)}}
			if !reflect.DeepEqual(copy.Options, want) {
				t.Fatalf("options=%#v want=%#v", copy.Options, want)
			}
		})
	}
	t.Run("literal_multibyte_position", func(t *testing.T) {
		copy := parseCopy(t, "/* é界 */ COPY t FROM STDIN BINARY")
		want := []CopyOption{{Name: "format", Value: "binary", HasValue: true, Argument: CopyArgumentScalar, Pos: Position{Offset: 30, Character: 28}}}
		if !reflect.DeepEqual(copy.Options, want) {
			t.Fatalf("options=%#v want=%#v", copy.Options, want)
		}
	})
	for _, tc := range []struct {
		name, sql string
		from      bool
		endpoint  CopyEndpoint
		options   []CopyOption
	}{
		{"modern_without_with", "COPY t FROM STDIN (FORMAT binary)", true, CopyStdin,
			[]CopyOption{{Name: "format", Value: "binary", HasValue: true, Argument: CopyArgumentScalar}}},
		{"modern_with", "COPY t FROM STDIN WITH (FORMAT binary)", true, CopyStdin,
			[]CopyOption{{Name: "format", Value: "binary", HasValue: true, Argument: CopyArgumentScalar}}},
		{"modern_quoted_value", "COPY t FROM STDIN WITH (FORMAT 'binary')", true, CopyStdin,
			[]CopyOption{{Name: "format", Value: "binary", HasValue: true, Argument: CopyArgumentScalar}}},
		{"generic_binary", "COPY t FROM STDIN (BINARY)", true, CopyStdin,
			[]CopyOption{{Name: "binary"}}},
		{"generic_binary_with", "COPY t FROM STDIN WITH (BINARY)", true, CopyStdin,
			[]CopyOption{{Name: "binary"}}},
		{"generic_quoted_binary", `COPY t FROM STDIN WITH ("binary")`, true, CopyStdin,
			[]CopyOption{{Name: "binary"}}},
		{"modern_text", "COPY t TO STDOUT WITH (FORMAT text)", false, CopyStdout,
			[]CopyOption{{Name: "format", Value: "text", HasValue: true, Argument: CopyArgumentScalar}}},
		{"modern_csv", "COPY t FROM STDIN (FORMAT csv, HEADER true)", true, CopyStdin,
			[]CopyOption{{Name: "format", Value: "csv", HasValue: true, Argument: CopyArgumentScalar}, {Name: "header", Value: "true", HasValue: true, Argument: CopyArgumentScalar}}},
		{"legacy_text_options", "COPY t FROM STDIN WITH HEADER DELIMITER '|' NULL 'NIL'", true, CopyStdin,
			[]CopyOption{{Name: "header"}, {Name: "delimiter", Value: "|", HasValue: true, Argument: CopyArgumentScalar}, {Name: "null", Value: "NIL", HasValue: true, Argument: CopyArgumentScalar}}},
		{"legacy_quoted_header", `COPY t FROM STDIN "header"`, true, CopyStdin,
			[]CopyOption{{Name: "header"}}},
		{"generic_with", "COPY t FROM STDIN (WITH 'kept')", true, CopyStdin,
			[]CopyOption{{Name: "with", Value: "kept", HasValue: true, Argument: CopyArgumentScalar}}},
		{"generic_exists", "COPY t FROM STDIN (EXISTS 'kept')", true, CopyStdin,
			[]CopyOption{{Name: "exists", Value: "kept", HasValue: true, Argument: CopyArgumentScalar}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := parseCopy(t, tc.sql)
			if copy.From != tc.from || copy.Endpoint != tc.endpoint {
				t.Fatalf("COPY direction/endpoint=%#v", copy)
			}
			want := append([]CopyOption(nil), tc.options...)
			for i := range want {
				token := strings.ToUpper(want[i].Name)
				if tc.name == "generic_quoted_binary" || tc.name == "legacy_quoted_header" {
					token = `"` + want[i].Name + `"`
				}
				want[i].Pos = position(tc.sql, token)
			}
			if !reflect.DeepEqual(copy.Options, want) {
				t.Fatalf("options=%#v want=%#v", copy.Options, want)
			}
		})
	}
	for _, q := range []string{
		"CREATE TABLE header (header text, copy bool)",
		"SELECT header, copy FROM header",
		`CREATE TABLE "binary" ("BINARY" int4)`,
		`SELECT "BINARY" FROM "binary"`,
		"SELECT 1 AS binary",
		"SELECT 1 binary",
	} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatalf("generic identifier legality lost for %q: statements=%#v err=%v", q, statements, err)
		}
	}
	// REL_18_6 kwlist.h classifies BINARY as TYPE_FUNC_NAME_KEYWORD with
	// BARE_LABEL. gram.y ColId excludes that category, unlike ColLabel and
	// BareColLabel: bare relation/column names stay invalid, labels stay legal.
	for _, q := range []string{
		"CREATE TABLE binary (binary int4, header text, copy bool)",
		"CREATE TABLE t (binary int4)",
		"SELECT 1 FROM binary",
		"COPY t (binary) FROM STDIN BINARY",
	} {
		offset := strings.Index(q, "binary")
		want := sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601",
			Message:  `syntax error at or near "binary"`,
			Position: strconv.Itoa(utf8.RuneCountInString(q[:offset]) + 1),
		}
		statements, err := Parse(q)
		var diagnostic *sqlstate.Error
		if statements != nil || !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatalf("identifier boundary for %q: statements=%#v diagnostic=%#v want=%#v err=%v", q, statements, diagnostic, want, err)
		}
	}
}

func TestCopyParserLegacyBinaryDiagnostics(t *testing.T) {
	// Shared public cases use the PostgreSQL 18.6 records captured before the
	// grammar transfer. Unicode escape forms additionally exercise the scanner's
	// existing raw-token diagnostic contract. Compare every logical field.
	for _, with := range []string{"", "WITH "} {
		prefix := `/* é界 */ COPY public."Legacy Diagnostics" ("i") FROM STDIN ` + with
		for _, tc := range []struct {
			name, tail, token, message string
		}{
			{"quoted_lowercase", `"binary"`, `"binary"`, `syntax error at or near ""binary""`},
			{"quoted_uppercase", `"BINARY"`, `"BINARY"`, `syntax error at or near ""BINARY""`},
			{"quoted_unicode", `"bínary"`, `"bínary"`, `syntax error at or near ""bínary""`},
			{"quoted_unicode_escape", `U&"b\0069nary"`, `U&"b\0069nary"`, `syntax error at or near "U&"b\0069nary""`},
			{"quoted_unicode_uppercase", `U&"BINARY"`, `U&"BINARY"`, `syntax error at or near "U&"BINARY""`},
			{"binary_string_argument", "BINARY 'x'", "'x'", `syntax error at or near "'x'"`},
			{"mixed_case_string_argument", "BiNaRy 'x'", "'x'", `syntax error at or near "'x'"`},
			{"unknown_option", "unknown", "unknown", `syntax error at or near "unknown"`},
			{"trailing_word", "BINARY garbage", "garbage", `syntax error at or near "garbage"`},
			{"junk_after_semicolon", "BINARY; garbage", "garbage", `syntax error at or near "garbage"`},
			{"malformed_later_statement", "BINARY; SELECT 1 +", "", "syntax error at end of input"},
			{"malformed_later_create", "BINARY; CREATE TABLE legacy_incomplete (", "", "syntax error at end of input"},
			{"malformed_where", "BINARY WHERE", "", "syntax error at end of input"},
			{"malformed_where_tail", "BINARY WHERE TRUE garbage", "garbage", `syntax error at or near "garbage"`},
		} {
			t.Run(with+tc.name, func(t *testing.T) {
				q := prefix + tc.tail
				offset := len(q)
				if tc.token != "" {
					offset = strings.LastIndex(q, tc.token)
					if offset < len(prefix) {
						t.Fatalf("missing diagnostic token %q in tail %q", tc.token, tc.tail)
					}
				}
				want := sqlstate.Error{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message,
					Position: strconv.Itoa(utf8.RuneCountInString(q[:offset]) + 1),
				}
				statements, err := Parse(q)
				var diagnostic *sqlstate.Error
				if statements != nil || !errors.As(err, &diagnostic) || *diagnostic != want {
					t.Fatalf("Parse(%q): statements=%#v diagnostic=%#v want=%#v err=%v", q, statements, diagnostic, want, err)
				}
			})
		}
	}
}
