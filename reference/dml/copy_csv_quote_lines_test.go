package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCopyCSVEqualNewlineQuote(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_csv_quote_lines(v text)")
		c.exec(t, "INSERT INTO copy_csv_quote_lines VALUES('sentinel')")
		for _, tc := range []struct {
			name, option, input string
			want                fixture.Diagnostic
		}{
			{"cr-default-escape", "QUOTE E'\\r'", "\rbad\r\rvalue\r", fixture.Diagnostic{Code: "22P04", Message: "unterminated CSV quoted field", Context: "COPY copy_csv_quote_lines, line 2: \"\rbad\""}},
			{"lf-default-escape", "QUOTE E'\\n'", "\nbad\n\nvalue\n", fixture.Diagnostic{Code: "22P04", Message: "unterminated CSV quoted field", Context: "COPY copy_csv_quote_lines, line 1: \"\nbad\""}},
			{"cr-explicit-equal-escape", "QUOTE E'\\r',ESCAPE E'\\r'", "\rbad\r\rvalue\r", fixture.Diagnostic{Code: "22P04", Message: "unterminated CSV quoted field", Context: "COPY copy_csv_quote_lines, line 2: \"\rbad\""}},
			{"lf-explicit-equal-escape", "QUOTE E'\\n',ESCAPE E'\\n'", "\nbad\n\nvalue\n", fixture.Diagnostic{Code: "22P04", Message: "unterminated CSV quoted field", Context: "COPY copy_csv_quote_lines, line 1: \"\nbad\""}},
			{"cr-after-staged-lf-row", "QUOTE E'\\r'", "plain\n\rbad\r\rvalue\r", fixture.Diagnostic{Code: "22P04", Message: "unquoted carriage return found in data", Hint: "Use quoted CSV field to represent carriage return.", Context: "COPY copy_csv_quote_lines, line 2"}},
			{"lf-after-staged-cr-row", "QUOTE E'\\n'", "plain\r\nbad\n\nvalue\n", fixture.Diagnostic{Code: "22P04", Message: "unquoted newline found in data", Hint: "Use quoted CSV field to represent newline.", Context: "COPY copy_csv_quote_lines, line 2"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, widths := range [][]int{{1}, {2, 1, 5, 3}} {
					result, err := c.from("COPY copy_csv_quote_lines FROM STDIN WITH(FORMAT csv,"+tc.option+")", &copyTextChunks{text: tc.input, widths: widths})
					copyTextDiagnostic(t, result, err, tc.want)
					c.selectRows(t, "SELECT v FROM copy_csv_quote_lines", []string{"v"}, []string{"TEXT"}, [][]any{{"sentinel"}})
				}
			})
		}
	})
}
