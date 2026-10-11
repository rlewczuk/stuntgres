package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCopyTextOptionsAndDefaults(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_defaults(id int,d int DEFAULT 7,v text DEFAULT 'fallback')")
		result, err := c.from("COPY copy_defaults(v,id) FROM STDIN WITH(FORMAT text,DELIMITER '|',NULL 'NIL',DEFAULT 'DEF')", strings.NewReader("DEF|1\nexplicit|2\nNIL|3\n|4\n"))
		copyTextSuccess(t, result, err, 4)
		c.selectRows(t, "SELECT * FROM copy_defaults ORDER BY id", []string{"id", "d", "v"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(7), "fallback"}, {int64(2), int64(7), "explicit"}, {int64(3), int64(7), nil}, {int64(4), int64(7), ""}})
		c.output(t, "COPY (SELECT id,v FROM copy_defaults ORDER BY id) TO STDOUT WITH(DELIMITER '|',NULL 'NIL')", "1|fallback\n2|explicit\n3|NIL\n4|\n", 4)
		c.exec(t, "CREATE TABLE copy_markers(v text DEFAULT NULL)")
		result, err = c.from("COPY copy_markers FROM STDIN WITH(DEFAULT 'DEF')", strings.NewReader("DEF\n"))
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22P04", Message: "unexpected default marker in COPY data", Detail: `Column "v" has no default value.`, Context: `COPY copy_markers, line 1: "DEF"`})
		c.selectRows(t, "SELECT * FROM copy_markers", []string{"v"}, []string{"TEXT"}, [][]any{})
		result, err = c.from("COPY copy_markers FROM STDIN WITH(DEFAULT 'DEF')", strings.NewReader("\\104EF\n\\N\n"))
		copyTextSuccess(t, result, err, 2)
		c.selectRows(t, "SELECT * FROM copy_markers ORDER BY v NULLS FIRST", []string{"v"}, []string{"TEXT"}, [][]any{{nil}, {"DEF"}})
		c.exec(t, "CREATE TABLE copy_no_default(v text)")
		result, err = c.from("COPY copy_no_default FROM STDIN WITH(DEFAULT 'DEF')", strings.NewReader("DEF\n"))
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22P04", Message: "unexpected default marker in COPY data", Detail: `Column "v" has no default value.`, Context: `COPY copy_no_default, line 1: "DEF"`})
		c.exec(t, "CREATE TABLE copy_ambiguous(v text)")
		result, err = c.from("COPY copy_ambiguous FROM STDIN WITH(NULL '')", strings.NewReader("\n"))
		copyTextSuccess(t, result, err, 1)
		c.selectRows(t, "SELECT * FROM copy_ambiguous", []string{"v"}, []string{"TEXT"}, [][]any{{nil}})
		c.output(t, "COPY copy_ambiguous TO STDOUT WITH(NULL '')", "\n", 1)
		c.exec(t, "CREATE TABLE copy_delimiter(v text)")
		result, err = c.from("COPY copy_delimiter FROM STDIN DELIMITER '|' NULL 'NIL'", strings.NewReader("a\\|b\n"))
		copyTextSuccess(t, result, err, 1)
		c.output(t, "COPY copy_delimiter TO STDOUT DELIMITER '|' NULL 'NIL'", "a\\|b\n", 1)
		c.exec(t, "CREATE TABLE copy_raw_markers(id int,v text DEFAULT 'fallback')")
		result, err = c.from(`COPY copy_raw_markers FROM STDIN WITH(NULL '\x00',DEFAULT '\x01')`, strings.NewReader("1\t"+`\x00`+"\n2\t"+`\x01`+"\n3\t"+`\\x00`+"\n"))
		copyTextSuccess(t, result, err, 3)
		c.selectRows(t, "SELECT * FROM copy_raw_markers ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), "fallback"}, {int64(3), `\x00`}})
	})
}

func TestReferenceCopyTextHeader(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_header(id int,v text)")
		result, err := c.from("COPY copy_header(v,id) FROM STDIN WITH(HEADER MATCH)", strings.NewReader("v\tid\nvalue\t1\n"))
		copyTextSuccess(t, result, err, 1)
		c.output(t, "COPY (SELECT v,id FROM copy_header ORDER BY id) TO STDOUT WITH(HEADER true)", "v\tid\nvalue\t1\n", 1)
		for _, tc := range []struct {
			input string
			want  fixture.Diagnostic
		}{
			{"v\n", fixture.Diagnostic{Code: "22P04", Message: "wrong number of fields in header line: got 1, expected 2", Context: `COPY copy_header, line 1: "v"`}},
			{"id\tv\n", fixture.Diagnostic{Code: "22P04", Message: `column name mismatch in header line field 1: got "id", expected "v"`, Context: "COPY copy_header, line 1: \"id\tv\""}},
			{"\\N\tid\n", fixture.Diagnostic{Code: "22P04", Message: `column name mismatch in header line field 1: got null value ("\N"), expected "v"`, Context: `COPY copy_header, line 1: "\N` + "\tid\""}},
		} {
			t.Run(tc.input, func(t *testing.T) {
				result, err := c.from("COPY copy_header(v,id) FROM STDIN WITH(HEADER MATCH)", strings.NewReader(tc.input))
				copyTextDiagnostic(t, result, err, tc.want)
			})
		}
		result, err = c.from("COPY copy_header FROM STDIN WITH(HEADER)", strings.NewReader("ignored\n2\tsecond\n"))
		copyTextSuccess(t, result, err, 1)
		for _, tc := range []struct {
			name, option string
			rejected     bool
		}{
			{"true", "true", false},
			{"integer_one", "1", false},
			{"on", "on", false},
			{"yes", "yes", true},
			{"quoted_one", "'1'", true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				result, err := c.from("COPY copy_header FROM STDIN WITH(HEADER "+tc.option+")", strings.NewReader("id\tv\n"))
				if tc.rejected {
					copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "42601", Message: `header requires a Boolean value or "match"`})
				} else {
					copyTextSuccess(t, result, err, 0)
				}
			})
		}
		result, err = c.from("COPY copy_header FROM STDIN WITH(HEADER false)", strings.NewReader("3\tthird\n"))
		copyTextSuccess(t, result, err, 1)
		c.exec(t, "CREATE TABLE copy_header_empty(v text)")
		for _, tc := range []struct {
			name, input string
			rejected    bool
		}{
			{"empty_input", "", true},
			{"header_eof", "v", false},
			{"header_newline", "v\n", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				result, err := c.from("COPY copy_header_empty FROM STDIN WITH(HEADER MATCH)", strings.NewReader(tc.input))
				if tc.rejected {
					copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22P04", Message: `column name mismatch in header line field 1: got "", expected "v"`, Context: `COPY copy_header_empty, line 1: ""`})
				} else {
					copyTextSuccess(t, result, err, 0)
				}
			})
		}
		c.exec(t, "CREATE TABLE copy_header_escaped(\"a\tb\" text,\"c\\d\" text,\"e\nf\" text)")
		c.output(t, "COPY copy_header_escaped TO STDOUT WITH(HEADER)", "a\\tb\tc\\\\d\te\\nf\n", 0)
		result, err = c.from("COPY copy_header_escaped FROM STDIN WITH(HEADER MATCH)", strings.NewReader("a\\tb\tc\\\\d\te\\nf\n1\t2\t3\n"))
		copyTextSuccess(t, result, err, 1)
	})
}

func TestReferenceCopyTextOptionsDiagnostics(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_options(v text)")
		for _, tc := range []struct {
			options, code, message string
			to                     bool
			position               string
		}{
			{"DELIMITER 'ab'", "0A000", "COPY delimiter must be a single one-byte character", false, ""},
			{"DELIMITER 'é'", "0A000", "COPY delimiter must be a single one-byte character", false, ""},
			{"DELIMITER E'\\n'", "22023", "COPY delimiter cannot be newline or carriage return", false, ""},
			{"DELIMITER E'\\\\'", "22023", `COPY delimiter cannot be "\"`, false, ""},
			{"DELIMITER 'a'", "22023", `COPY delimiter cannot be "a"`, false, ""},
			{"NULL E'\\n'", "22023", "COPY null representation cannot use newline or carriage return", false, ""},
			{"DEFAULT E'\\r'", "22023", "COPY default representation cannot use newline or carriage return", false, ""},
			{"DELIMITER '|',NULL 'a|b'", "22023", "COPY delimiter character must not appear in the NULL specification", false, ""},
			{"DELIMITER '|',DEFAULT 'a|b'", "0A000", "COPY delimiter character must not appear in the DEFAULT specification", false, ""},
			{"NULL 'same',DEFAULT 'same'", "0A000", "NULL specification and DEFAULT specification cannot be the same", false, ""},
			{"DEFAULT 'x'", "0A000", "COPY DEFAULT cannot be used with COPY TO", true, ""},
			{"HEADER MATCH", "0A000", `cannot use "match" with HEADER in COPY TO`, true, ""},
			{"HEADER 'wrong'", "42601", `header requires a Boolean value or "match"`, false, ""},
			{"QUOTE 'x'", "0A000", "COPY QUOTE requires CSV mode", false, ""},
			{"ESCAPE 'x'", "0A000", "COPY ESCAPE requires CSV mode", false, ""},
			{"FORCE_QUOTE *", "0A000", "COPY FORCE_QUOTE requires CSV mode", false, ""},
			{"FORCE_NOT_NULL(v)", "0A000", "COPY FORCE_NOT_NULL requires CSV mode", false, ""},
			{"FORCE_NULL(v)", "0A000", "COPY FORCE_NULL requires CSV mode", false, ""},
			{"FORMAT wrong", "22023", `COPY format "wrong" not recognized`, false, "35"},
			{"UNKNOWN 'x'", "42601", `option "unknown" not recognized`, false, "35"},
			{"HEADER true,HEADER false", "42601", "conflicting or redundant options", false, "47"},
		} {
			t.Run(tc.options, func(t *testing.T) {
				q := "COPY copy_options FROM STDIN WITH(" + tc.options + ")"
				var result copyTextResult
				var err error
				if tc.to {
					q = "COPY copy_options TO STDOUT WITH(" + tc.options + ")"
					result, err = c.to(q, &strings.Builder{})
				} else {
					result, err = c.from(q, strings.NewReader(""))
				}
				// Option positions are SQL character offsets, not byte offsets.
				want := fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position}
				copyTextDiagnostic(t, result, err, want)
			})
		}
	})
}
