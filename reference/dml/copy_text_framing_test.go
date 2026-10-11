package reference_test

import (
	"io"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

type copyTextChunks struct {
	text         string
	widths       []int
	offset, step int
}

func (r *copyTextChunks) Read(p []byte) (int, error) {
	if r.offset == len(r.text) {
		return 0, io.EOF
	}
	n := r.widths[r.step%len(r.widths)]
	r.step++
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.text)-r.offset {
		n = len(r.text) - r.offset
	}
	copy(p, r.text[r.offset:r.offset+n])
	r.offset += n
	return n, nil
}
func TestReferenceCopyTextFraming(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		for _, tc := range []struct {
			name, input string
			want        []string
		}{
			{"lf", "a\nb\n", []string{"a", "b"}},
			{"cr", "a\rb\r", []string{"a", "b"}},
			{"crlf", "a\r\nb\r\n", []string{"a", "b"}},
			{"eof", "final", []string{"final"}},
			{"empty", "", []string{}},
			{"escaped", "a\\\nb\nc\\\rd\n", []string{"a\nb", "c\rd"}},
			{"escapes", `\141\x62\q\12\x9` + "\n", []string{"abq\n\t"}},
			{"utf8", "é漢\n", []string{"é漢"}},
			{"large", strings.Repeat("x", 100000) + "\n", []string{strings.Repeat("x", 100000)}},
			{"terminator", "first\n\\.\nignored\n", []string{"first"}},
			{"trailing_slash", "slash\\", []string{"slash"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, widths := range [][]int{{1}, {3, 1, 2, 7, 5}} {
					c.exec(t, "CREATE TABLE copy_frame(v text)")
					t.Cleanup(func() {
						if _, err := c.conn.ExecContext(c.ctx, "DROP TABLE IF EXISTS copy_frame"); err != nil {
							t.Errorf("framing table cleanup: %v", err)
						}
					})
					result, err := c.from("COPY copy_frame FROM STDIN", &copyTextChunks{text: tc.input, widths: widths})
					copyTextSuccess(t, result, err, int64(len(tc.want)))
					want := make([][]any, 0, len(tc.want))
					for _, v := range tc.want {
						want = append(want, []any{v})
					}
					c.selectRows(t, "SELECT * FROM copy_frame ORDER BY v", []string{"v"}, []string{"TEXT"}, want)
					c.exec(t, "DROP TABLE copy_frame")
				}
			})
		}
		c.exec(t, "CREATE TABLE copy_frame_errors(v text)")
		for _, tc := range []struct {
			name, input string
			want        fixture.Diagnostic
		}{
			{"lf_cr", "a\nb\r", fixture.Diagnostic{Code: "22P04", Message: "literal carriage return found in data", Hint: `Use "\r" to represent carriage return.`, Context: "COPY copy_frame_errors, line 2"}},
			{"cr_lf", "a\rb\n", fixture.Diagnostic{Code: "22P04", Message: "literal newline found in data", Hint: `Use "\n" to represent newline.`, Context: "COPY copy_frame_errors, line 2"}},
			{"crlf_cr", "a\r\nb\r", fixture.Diagnostic{Code: "22P04", Message: "literal carriage return found in data", Hint: `Use "\r" to represent carriage return.`, Context: "COPY copy_frame_errors, line 2"}},
			{"marker_suffix", "a\n\\.x\n", fixture.Diagnostic{Code: "22P04", Message: "end-of-copy marker is not alone on its line", Context: "COPY copy_frame_errors, line 2"}},
			{"marker_prefix", "a\nx\\.\n", fixture.Diagnostic{Code: "22P04", Message: "end-of-copy marker is not alone on its line", Context: "COPY copy_frame_errors, line 2"}},
			{"marker_style", "a\n\\.\r", fixture.Diagnostic{Code: "22P04", Message: "end-of-copy marker does not match previous newline style", Context: "COPY copy_frame_errors, line 2"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				result, err := c.from("COPY copy_frame_errors FROM STDIN", &copyTextChunks{text: tc.input, widths: []int{1}})
				copyTextDiagnostic(t, result, err, tc.want)
				c.selectRows(t, "SELECT * FROM copy_frame_errors", []string{"v"}, []string{"TEXT"}, [][]any{})
			})
		}
	})
}
