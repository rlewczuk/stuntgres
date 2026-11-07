package reference_test

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyLegacyBinaryDiagnostics(t *testing.T) {
	// REL_18_6 gram.y copy_opt_item and copy.c ProcessCopyOptions specify
	// these independent expectations. The oracle must resolve the complete
	// records before the parser gate; assertion failures print every field.
	for _, with := range []struct {
		name, text string
	}{{"bare", ""}, {"with", "WITH "}} {
		t.Run(with.name, func(t *testing.T) {
			copyLegacyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
				c.exec(t, `CREATE TABLE public."Legacy Diagnostics" ("i" int4)`)
				c.exec(t, `INSERT INTO public."Legacy Diagnostics" VALUES (99)`)
				// The multibyte comment makes byte offsets differ from character
				// positions. The target and written column are genuinely quoted.
				prefix := `/* é界 */ COPY public."Legacy Diagnostics" ("i") FROM STDIN ` + with.text
				for _, tc := range []struct {
					name, tail, at string
					want           fixture.Diagnostic
				}{
					{
						name: "duplicate", tail: "BINARY BINARY", at: "BINARY",
						want: fixture.Diagnostic{Code: "42601", Message: "conflicting or redundant options"},
					},
					{
						name: "duplicate_mixed", tail: "BiNaRy bInArY", at: "bInArY",
						want: fixture.Diagnostic{Code: "42601", Message: "conflicting or redundant options"},
					},
					{
						name: "duplicate_before_incompatibility", tail: "BINARY DELIMITER '|' BINARY", at: "BINARY",
						want: fixture.Diagnostic{Code: "42601", Message: "conflicting or redundant options"},
					},
					{
						name: "delimiter_after", tail: "BINARY DELIMITER '|'",
						want: fixture.Diagnostic{Code: "42601", Message: "cannot specify DELIMITER in BINARY mode"},
					},
					{
						name: "delimiter_before", tail: "DELIMITER '|' BINARY",
						want: fixture.Diagnostic{Code: "42601", Message: "cannot specify DELIMITER in BINARY mode"},
					},
					{
						name: "null_after", tail: "BINARY NULL 'NIL'",
						want: fixture.Diagnostic{Code: "42601", Message: "cannot specify NULL in BINARY mode"},
					},
					{
						name: "null_before", tail: "NULL 'NIL' BINARY",
						want: fixture.Diagnostic{Code: "42601", Message: "cannot specify NULL in BINARY mode"},
					},
					{
						name: "header_after", tail: "BINARY HEADER",
						want: fixture.Diagnostic{Code: "0A000", Message: "cannot specify HEADER in BINARY mode"},
					},
					{
						name: "header_before", tail: "HEADER BINARY",
						want: fixture.Diagnostic{Code: "0A000", Message: "cannot specify HEADER in BINARY mode"},
					},
					{
						name: "quoted_lowercase", tail: `"binary"`, at: `"binary"`,
						want: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ""binary""`},
					},
					{
						name: "quoted_uppercase", tail: `"BINARY"`, at: `"BINARY"`,
						want: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ""BINARY""`},
					},
					{
						name: "quoted_unicode", tail: `"bínary"`, at: `"bínary"`,
						want: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ""bínary""`},
					},
					{
						name: "binary_string_argument", tail: "BINARY 'x'", at: "'x'",
						want: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "'x'"`},
					},
					{
						name: "generic_parenthesized_not_legacy", tail: "(BINARY)", at: "BINARY",
						want: fixture.Diagnostic{Code: "42601", Message: `option "binary" not recognized`},
					},
					{
						name: "trailing_word", tail: "BINARY garbage", at: "garbage",
						want: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "garbage"`},
					},
					{
						name: "junk_after_semicolon", tail: "BINARY; garbage", at: "garbage",
						want: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "garbage"`},
					},
					{
						name: "malformed_later_statement", tail: "BINARY; SELECT 1 +", at: "$end",
						want: fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input"},
					},
					{
						name: "malformed_later_create", tail: "BINARY; CREATE TABLE legacy_incomplete (", at: "$end",
						want: fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input"},
					},
					{
						name: "malformed_where", tail: "BINARY WHERE", at: "$end",
						want: fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input"},
					},
				} {
					t.Run(tc.name, func(t *testing.T) {
						sql := prefix + tc.tail
						want := tc.want
						if tc.at != "" {
							// Duplicate errors point at the last (second) option;
							// syntax errors point at the actual offending lexeme.
							offset := len(sql)
							if tc.at != "$end" {
								offset = strings.LastIndex(sql, tc.at)
								if offset < len(prefix) {
									t.Fatalf("diagnostic token %q not in tail %q", tc.at, tc.tail)
								}
							}
							want.Position = strconv.Itoa(utf8.RuneCountInString(sql[:offset]) + 1)
						}
						input := copyBinaryStream(copyBinaryRow(copyBinary32(1)))
						result, err := c.from(sql, bytes.NewReader(input))
						copyTextDiagnostic(t, result, err, want)
						c.selectRows(t, `SELECT * FROM public."Legacy Diagnostics" ORDER BY "i"`,
							[]string{"i"}, []string{"INT4"}, [][]any{{int64(99)}})
						c.selectRows(t, "SELECT 42::int4 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(42)}})
					})
				}
			})
		})
	}
}
