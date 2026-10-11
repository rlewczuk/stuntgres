package reference_test

import (
	"bytes"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceCopyCSVLineCounting(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_csv_lines(id int,v text)")
		c.exec(t, "INSERT INTO copy_csv_lines VALUES(99,'sentinel')")
		for _, tc := range []struct {
			input string
			line  int
			raw   string
		}{
			{"1,\"open\nfield", 1, "1,\"open\nfield"},
			{"1,ok\n2,\"open\nfield", 3, "2,\"open\nfield"},
			{"1,\"open\rfield", 2, "1,\"open\rfield"},
			{"1,ok\r\n2,\"open\r\nfield", 3, "2,\"open\r\nfield"},
		} {
			result, err := c.from("COPY copy_csv_lines FROM STDIN WITH(FORMAT csv)", strings.NewReader(tc.input))
			copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22P04", Message: "unterminated CSV quoted field", Context: "COPY copy_csv_lines, line " + strconv.Itoa(tc.line) + ": \"" + tc.raw + "\""})
			c.selectRows(t, "SELECT * FROM copy_csv_lines ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
		}
		c.exec(t, "CREATE TABLE copy_csv_header_lines(\"a\nb\" text,id int)")
		result, err := c.from("COPY copy_csv_header_lines FROM STDIN WITH(FORMAT csv,HEADER MATCH)", strings.NewReader("\"a\nb\",id\nvalue,bad\n"))
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type integer: \"bad\"", Context: "COPY copy_csv_header_lines, line 2, column id: \"bad\""})
		result, err = c.from("COPY copy_csv_lines FROM STDIN WITH(FORMAT csv,HEADER MATCH)", strings.NewReader("id,v"))
		copyTextSuccess(t, result, err, 0)
	})
}

func TestReferenceCopyCSVAdmissionAndFormat(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_csv_admit(id int,v text)")
		for _, tc := range []struct{ q, code, message string }{
			{"COPY copy_csv_missing FROM STDIN WITH(FORMAT csv,QUOTE 'xx')", "42P01", "relation \"copy_csv_missing\" does not exist"},
			{"COPY copy_csv_admit(missing) FROM STDIN WITH(FORMAT csv,QUOTE 'xx')", "42703", "column \"missing\" of relation \"copy_csv_admit\" does not exist"},
			{"COPY (SELECT * FROM copy_csv_missing) TO STDOUT WITH(FORMAT csv,QUOTE 'xx')", "0A000", "COPY quote must be a single one-byte character"},
		} {
			var result copyTextResult
			var err error
			never := &copyTextNoRead{}
			var out bytes.Buffer
			if strings.Contains(tc.q, " TO STDOUT") {
				result, err = c.to(tc.q, &out)
			} else {
				result, err = c.from(tc.q, never)
			}
			want := fixture.Diagnostic{Code: tc.code, Message: tc.message}
			copyTextDiagnostic(t, result, err, want)
			if !b.PG && never.calls != 0 || out.Len() != 0 {
				t.Fatal("failed admission touched stream")
			}
		}
		for _, value := range []string{"CSV", "Csv"} {
			q := "COPY copy_csv_admit FROM STDIN WITH(FORMAT '" + value + "')"
			never := &copyTextNoRead{}
			result, err := c.from(q, never)
			copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22023", Message: "COPY format \"" + value + "\" not recognized", Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "FORMAT")]) + 1)})
			if !b.PG && never.calls != 0 {
				t.Fatal("bad format read caller")
			}
		}
		for _, value := range []string{"csv", "'csv'", "CSV"} {
			result, err := c.from("COPY copy_csv_admit FROM STDIN WITH(FORMAT "+value+")", strings.NewReader("1,plain\n"))
			copyTextSuccess(t, result, err, 1)
			c.output(t, "COPY copy_csv_admit TO STDOUT WITH(FORMAT "+value+")", "1,plain\n", 1)
			c.exec(t, "DELETE FROM copy_csv_admit")
		}
		c.exec(t, "CREATE ROLE copy_csv_actor")
		if b.PG {
			c.exec(t, "GRANT USAGE ON SCHEMA public TO copy_csv_actor")
		}
		c.exec(t, "SET ROLE copy_csv_actor")
		never := &copyTextNoRead{}
		result, err := c.from("COPY copy_csv_admit FROM STDIN WITH(FORMAT csv,QUOTE 'xx')", never)
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "42501", Message: "permission denied for table copy_csv_admit"})
		if !b.PG && never.calls != 0 {
			t.Fatal("denied input read caller")
		}
		result, err = c.to("COPY copy_csv_admit TO STDOUT WITH(FORMAT csv,QUOTE 'xx')", &bytes.Buffer{})
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "42501", Message: "permission denied for table copy_csv_admit"})
		c.exec(t, "RESET ROLE")
	})
}
