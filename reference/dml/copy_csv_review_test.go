package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCopyCSVSeparatorTransitions(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_csv_separators(id int,v text)")
		c.exec(t, "INSERT INTO copy_csv_separators VALUES(99,'sentinel')")
		for _, tc := range []struct {
			name, input string
			want        fixture.Diagnostic
		}{
			{"established-cr-leaves-lf", "1,ok\r2,x\r\n", fixture.Diagnostic{Code: "22P04", Message: "unquoted newline found in data", Hint: "Use quoted CSV field to represent newline.", Context: "COPY copy_csv_separators, line 3"}},
			{"conversion-before-leftover-lf", "1,ok\rbad,x\r\n", fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type integer: \"bad\"", Context: "COPY copy_csv_separators, line 2, column id: \"bad\""}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, widths := range [][]int{{1}, {3, 1, 2, 7}} {
					result, err := c.from("COPY copy_csv_separators FROM STDIN WITH(FORMAT csv)", &copyTextChunks{text: tc.input, widths: widths})
					copyTextDiagnostic(t, result, err, tc.want)
					c.selectRows(t, "SELECT * FROM copy_csv_separators ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
				}
			})
		}
	})
}

func TestReferenceCopyCSVEscapedLineCounting(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_csv_escape_lines(id int,v text)")
		c.exec(t, "INSERT INTO copy_csv_escape_lines VALUES(99,'sentinel')")
		for _, tc := range []struct{ name, option, input, message, context string }{
			{"lf-pair", "ESCAPE E'\\n'", "1,ok\n\"bad\n\nvalue\",x\n", "invalid input syntax for type integer: \"bad\nvalue\"", "COPY copy_csv_escape_lines, line 4, column id: \"bad\nvalue\""},
			{"lf-before-quote", "ESCAPE E'\\n'", "1,ok\n\"bad\n\"value\",x\n", "invalid input syntax for type integer: \"bad\"value\"", "COPY copy_csv_escape_lines, line 3, column id: \"bad\"value\""},
			{"cr-pair", "ESCAPE E'\\r'", "1,ok\r\"bad\r\rvalue\",x\r", "invalid input syntax for type integer: \"bad\rvalue\"", "COPY copy_csv_escape_lines, line 4, column id: \"bad\rvalue\""},
			{"cr-before-quote", "ESCAPE E'\\r'", "1,ok\r\"bad\r\"value\",x\r", "invalid input syntax for type integer: \"bad\"value\"", "COPY copy_csv_escape_lines, line 3, column id: \"bad\"value\""},
			{"initial-lf-pair", "ESCAPE E'\\n'", "\"bad\n\nvalue\",x\n", "invalid input syntax for type integer: \"bad\nvalue\"", "COPY copy_csv_escape_lines, line 1, column id: \"bad\nvalue\""},
			{"initial-cr-pair", "ESCAPE E'\\r'", "\"bad\r\rvalue\",x\r", "invalid input syntax for type integer: \"bad\rvalue\"", "COPY copy_csv_escape_lines, line 3, column id: \"bad\rvalue\""},
			{"subsequent-record", "ESCAPE E'\\n'", "1,ok\n2,\"good\n\nvalue\"\nbad,x\n", "invalid input syntax for type integer: \"bad\"", "COPY copy_csv_escape_lines, line 5, column id: \"bad\""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, widths := range [][]int{{1}, {2, 1, 5, 3}} {
					result, err := c.from("COPY copy_csv_escape_lines FROM STDIN WITH(FORMAT csv,"+tc.option+")", &copyTextChunks{text: tc.input, widths: widths})
					copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22P02", Message: tc.message, Context: tc.context})
					c.selectRows(t, "SELECT * FROM copy_csv_escape_lines ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
				}
			})
		}
		c.exec(t, "CREATE TABLE copy_csv_escape_values(id int,v text)")
		for _, tc := range []struct{ option, input, value, output string }{
			{"ESCAPE E'\\n'", "1,\"good\n\nvalue\"\n", "good\nvalue", "1,\"good\nvalue\"\n"},
			{"ESCAPE E'\\r'", "1,\"good\r\rvalue\"\r", "good\rvalue", "1,\"good\rvalue\"\n"},
		} {
			result, err := c.from("COPY copy_csv_escape_values FROM STDIN WITH(FORMAT csv,"+tc.option+")", strings.NewReader(tc.input))
			copyTextSuccess(t, result, err, 1)
			c.selectRows(t, "SELECT * FROM copy_csv_escape_values", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), tc.value}})
			c.output(t, "COPY copy_csv_escape_values TO STDOUT WITH(FORMAT csv)", tc.output, 1)
			c.exec(t, "DELETE FROM copy_csv_escape_values")
		}
	})
}
