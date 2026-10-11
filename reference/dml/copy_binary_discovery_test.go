package reference_test

import (
	"bytes"
	"testing"
)

func TestReferenceCopyBinaryHeaderDiagnostics(t *testing.T) {
	signature := copyBinaryHex("5047434f50590aff0d0a00")
	cases := []struct {
		name  string
		input []byte
	}{
		{"empty", nil}, {"short_signature", signature[:10]},
		{"wrong_signature", []byte("PGCOPY\n!\r\n\x00")},
		{"missing_flags", signature},
		{"partial_flags", append(append([]byte{}, signature...), 0, 0, 0)},
		{"with_oids", copyBinaryHeader(1<<16, 0, nil)},
		{"with_oids_and_unknown_critical", copyBinaryHeader(3<<16, 0, nil)},
		{"critical_flags", copyBinaryHeader(1<<17, 0, nil)},
		{"missing_extension_length", append(append([]byte{}, signature...), copyBinary32(0)...)},
		{"partial_extension_length", append(copyBinaryHeader(0, 0, nil)[:15], 0, 0, 0)},
		{"negative_extension_length", copyBinaryHeader(0, -1, nil)},
		{"truncated_extension", copyBinaryHeader(0, 3, []byte{1, 2})},
		{"ignored_low_flags", append(copyBinaryHeader(0xffff, 0, nil), copyBinary16(-1)...)},
		{"skipped_extension", append(copyBinaryHeader(0, 3, []byte{0, 255, 1}), copyBinary16(-1)...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
				c.exec(t, "CREATE TABLE copy_binary_probe(v int4)")
				c.exec(t, "INSERT INTO copy_binary_probe VALUES(99)")
				result, err := c.from("COPY copy_binary_probe FROM STDIN WITH(FORMAT binary)", bytes.NewReader(tc.input))
				copyBinaryOutcome(t, "HeaderDiagnostics", tc.name, result, err, nil)
				c.selectRows(t, "SELECT * FROM copy_binary_probe", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})
			})
		})
	}
}

func TestReferenceCopyBinaryEOFBehavior(t *testing.T) {
	first := copyBinaryRow(copyBinary32(42))
	prefix := append(copyBinaryHeader(0, 0, nil), first...)
	cases := []struct {
		name   string
		suffix []byte
	}{
		{"no_trailer", nil}, {"partial_next_count_zero", []byte{0}}, {"partial_next_count_ff", []byte{255}},
		{"trailer", copyBinary16(-1)}, {"trailer_then_byte", []byte{255, 255, 0}},
		{"zero_arity", copyBinary16(0)}, {"negative_arity", copyBinary16(-2)}, {"wrong_arity", copyBinary16(2)},
		{"missing_field_length", copyBinary16(1)},
		{"partial_field_length", []byte{0, 1, 0, 0, 0}},
		{"negative_field_length", append(copyBinary16(1), copyBinary32(-2)...)},
		// Bounded prefixes only: no allocation driven by these announced lengths.
		{"huge_field_length", append(copyBinary16(1), copyBinary32(0x7fffffff)...)},
		{"truncated_payload", append(append(copyBinary16(1), copyBinary32(4)...), 0, 0, 0)},
		{"short_complete_int4", append(copyBinaryRow([]byte{0, 0, 0}), copyBinary16(-1)...)},
		{"overlong_int4", append(copyBinaryRow([]byte{0, 0, 0, 42, 0}), copyBinary16(-1)...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
				c.exec(t, "CREATE TABLE copy_binary_probe(v int4)")
				c.exec(t, "INSERT INTO copy_binary_probe VALUES(99)")
				input := append(append([]byte{}, prefix...), tc.suffix...)
				reader := &copyTextChunks{text: string(input), widths: []int{1}}
				result, err := c.from("COPY copy_binary_probe FROM STDIN WITH(FORMAT binary)", reader)
				// This assertion is client-reader consumption, not server parser position.
				if (c.pg || err == nil) && reader.offset != len(input) {
					t.Errorf("reader consumed=%d supplied=%d", reader.offset, len(input))
				}
				copyBinaryOutcome(t, "EOFBehavior", tc.name, result, err, nil)
				if err != nil {
					c.selectRows(t, "SELECT * FROM copy_binary_probe", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})
				} else {
					// Independently require COPY 1 and the actual first row for permissive EOF.
					copyTextSuccess(t, result, err, 1)
					c.selectRows(t, "SELECT * FROM copy_binary_probe ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}, {int64(99)}})
				}
			})
		})
	}
}

func TestReferenceCopyBinaryOptions(t *testing.T) {
	options := []struct{ name, sql string }{
		{"header_false", "FORMAT binary, HEADER false"}, {"header_true", "FORMAT binary, HEADER true"},
		{"header_match", "FORMAT binary, HEADER MATCH"}, {"header_bare", "FORMAT binary, HEADER"},
		{"delimiter", "FORMAT binary, DELIMITER ','"}, {"null", "FORMAT binary, NULL 'N'"},
		{"default", "FORMAT binary, DEFAULT 'D'"}, {"quote", "FORMAT binary, QUOTE '!'"},
		{"escape", "FORMAT binary, ESCAPE '!'"}, {"force_quote", "FORMAT binary, FORCE_QUOTE *"},
		{"force_null", "FORMAT binary, FORCE_NULL (v)"}, {"force_not_null", "FORMAT binary, FORCE_NOT_NULL (v)"},
		{"on_error_stop", "FORMAT binary, ON_ERROR stop"}, {"on_error_ignore", "FORMAT binary, ON_ERROR ignore"},
		{"encoding", "FORMAT binary, ENCODING 'UTF8'"},
		{"delimiter_before_header", "FORMAT binary, DELIMITER ',', HEADER true"},
		{"header_before_delimiter", "FORMAT binary, HEADER true, DELIMITER ','"},
		{"null_before_delimiter", "FORMAT binary, NULL 'N', DELIMITER ','"},
		{"delimiter_before_null", "FORMAT binary, DELIMITER ',', NULL 'N'"},
		{"quote_before_header", "FORMAT binary, QUOTE '!', HEADER true"},
		{"duplicate_format", "FORMAT binary, FORMAT text"}, {"duplicate_header", "FORMAT binary, HEADER false, HEADER false"},
		{"unknown_option", "FORMAT binary, imaginary true"},
	}
	for _, tc := range options {
		for _, direction := range []string{"FROM", "TO"} {
			t.Run(tc.name+"_"+direction, func(t *testing.T) {
				copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
					c.exec(t, "CREATE TABLE copy_binary_probe(v int4)")
					query := "COPY copy_binary_probe " + direction + " STD"
					var result copyTextResult
					var err error
					var output bytes.Buffer
					if direction == "FROM" {
						query += "IN WITH(" + tc.sql + ")"
						result, err = c.from(query, bytes.NewReader(copyBinaryStream(copyBinaryRow(copyBinary32(42)))))
					} else {
						query += "OUT WITH(" + tc.sql + ")"
						result, err = c.to(query, &output)
					}
					copyBinaryOptionOutcome(t, c, tc.name, direction, query, result, err, output.Bytes())
					if direction == "FROM" && err != nil {
						copyBinaryCount(t, c, 0)
					}
					if direction == "FROM" && err == nil {
						copyTextSuccess(t, result, err, 1)
						c.selectRows(t, "SELECT * FROM copy_binary_probe", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
					}
				})
			})
		}
	}
}
