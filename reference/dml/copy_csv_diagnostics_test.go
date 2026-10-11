package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCopyCSVDiagnostics(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_csv_errors(id int,b text)")
		c.exec(t, "INSERT INTO copy_csv_errors VALUES(99,'sentinel')")
		for _, tc := range []struct{ name, input, code, message, context, hint string }{
			{"unterminated", "1,ok\n2,\"unfinished", "22P04", "unterminated CSV quoted field", "COPY copy_csv_errors, line 2: \"2,\"unfinished\"", ""},
			{"extra", "1,ok\nbad,x,y\n", "22P04", "extra data after last expected column", "COPY copy_csv_errors, line 2: \"bad,x,y\"", ""},
			{"missing", "1,ok\n2\n", "22P04", "missing data for column \"b\"", "COPY copy_csv_errors, line 2: \"2\"", ""},
			{"conversion-before-missing", "1,ok\nbad\n", "22P02", "invalid input syntax for type integer: \"bad\"", "COPY copy_csv_errors, line 2, column id: \"bad\"", ""},
			{"decoded", "1,ok\n\"bad\",x\n", "22P02", "invalid input syntax for type integer: \"bad\"", "COPY copy_csv_errors, line 2, column id: \"bad\"", ""},
			{"mixed-cr", "1,ok\n2,x\r", "22P04", "unquoted carriage return found in data", "COPY copy_csv_errors, line 2", "Use quoted CSV field to represent carriage return."},
			{"mixed-lf", "1,ok\r2,x\n", "22P04", "unquoted newline found in data", "COPY copy_csv_errors, line 2", "Use quoted CSV field to represent newline."},
			// Released parser-history line counts, not logical record numbering. Oracle checkpoint.
			{"initial-lf", "\"bad\nvalue\",x\n", "22P02", "invalid input syntax for type integer: \"bad\nvalue\"", "COPY copy_csv_errors, line 1, column id: \"bad\nvalue\"", ""},
			{"established-lf", "1,ok\n\"bad\nvalue\",x\n", "22P02", "invalid input syntax for type integer: \"bad\nvalue\"", "COPY copy_csv_errors, line 3, column id: \"bad\nvalue\"", ""},
			{"initial-cr", "\"bad\rvalue\",x\r", "22P02", "invalid input syntax for type integer: \"bad\rvalue\"", "COPY copy_csv_errors, line 2, column id: \"bad\rvalue\"", ""},
			{"established-crlf", "1,ok\r\n\"bad\r\nvalue\",x\r\n", "22P02", "invalid input syntax for type integer: \"bad\r\nvalue\"", "COPY copy_csv_errors, line 3, column id: \"bad\r\nvalue\"", ""},
			{"overflow", "1,ok\n2147483648,x\n", "22003", "value \"2147483648\" is out of range for type integer", "COPY copy_csv_errors, line 2, column id: \"2147483648\"", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				result, err := c.from("COPY copy_csv_errors FROM STDIN WITH(FORMAT csv)", &copyTextChunks{text: tc.input, widths: []int{1, 3, 2}})
				copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: tc.code, Message: tc.message, Context: tc.context, Hint: tc.hint})
				c.selectRows(t, "SELECT * FROM copy_csv_errors ORDER BY id", []string{"id", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
			})
		}
	})
}

func TestReferenceCopyCSVUnicodeContext(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_csv_unicode(id int)")
		c.exec(t, "INSERT INTO copy_csv_unicode VALUES(99)")
		for _, late := range []bool{false, true} {
			for _, raw := range []bool{false, true} {
				value := strings.Repeat("é", 60)
				input := "\"" + value + "\""
				line := 1
				want := fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type integer: \"" + value + "\""}
				if raw {
					input += ",x"
					want.Code = "22P04"
					want.Message = "extra data after last expected column"
				}
				input += "\n"
				if late {
					input = "1\n" + input
					line = 2
				}
				if raw {
					want.Context = fmt.Sprintf("COPY copy_csv_unicode, line %d: \"\"%s...\"", line, strings.Repeat("é", 49))
				} else {
					want.Context = fmt.Sprintf("COPY copy_csv_unicode, line %d, column id: \"%s...\"", line, strings.Repeat("é", 50))
				}
				result, err := c.from("COPY copy_csv_unicode FROM STDIN WITH(FORMAT csv)", strings.NewReader(input))
				copyTextDiagnostic(t, result, err, want)
				c.selectRows(t, "SELECT id FROM copy_csv_unicode ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}})
			}
		}
	})
}
