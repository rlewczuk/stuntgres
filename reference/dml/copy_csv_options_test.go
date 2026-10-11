package reference_test

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceCopyCSVHeader(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_csv_header(a text,b text)")
		result, err := c.from("COPY copy_csv_header(b,a) FROM STDIN WITH(FORMAT csv,HEADER MATCH)", strings.NewReader("b,a\nx,y\n"))
		copyTextSuccess(t, result, err, 1)
		c.selectRows(t, "SELECT * FROM copy_csv_header", []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"y", "x"}})
		for _, tc := range []struct{ header, message string }{
			{"a,b", "column name mismatch in header line field 1: got \"a\", expected \"b\""},
			{"b", "wrong number of fields in header line: got 1, expected 2"},
			{",a", "column name mismatch in header line field 1: got null value (\"\"), expected \"b\""},
			{"\"\",a", "column name mismatch in header line field 1: got \"\", expected \"b\""},
		} {
			for _, forces := range []string{"", ",FORCE_NULL(b),FORCE_NOT_NULL(b)"} {
				result, err = c.from("COPY copy_csv_header(b,a) FROM STDIN WITH(FORMAT csv,HEADER MATCH"+forces+")", strings.NewReader(tc.header+"\nz,z\n"))
				copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22P04", Message: tc.message, Context: "COPY copy_csv_header, line 1: \"" + tc.header + "\""})
				c.selectRows(t, "SELECT * FROM copy_csv_header", []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"y", "x"}})
			}
		}
		c.exec(t, "CREATE TABLE copy_csv_header_one(v text)")
		for _, option := range []string{"HEADER", "HEADER true", "HEADER on", "HEADER 1"} {
			result, err = c.from("COPY copy_csv_header_one FROM STDIN WITH(FORMAT csv,"+option+")", strings.NewReader("discard\nvalue\n"))
			copyTextSuccess(t, result, err, 1)
			c.output(t, "COPY copy_csv_header_one TO STDOUT WITH(FORMAT csv,HEADER true,FORCE_QUOTE *)", "v\n\"value\"\n", 1)
			c.exec(t, "DELETE FROM copy_csv_header_one")
			result, err = c.from("COPY copy_csv_header_one FROM STDIN WITH(FORMAT csv,"+option+")", strings.NewReader("header"))
			copyTextSuccess(t, result, err, 0)
		}
		for _, option := range []string{"HEADER false", "HEADER off", "HEADER 0"} {
			result, err = c.from("COPY copy_csv_header_one FROM STDIN WITH(FORMAT csv,"+option+")", strings.NewReader("value\n"))
			copyTextSuccess(t, result, err, 1)
			c.exec(t, "DELETE FROM copy_csv_header_one")
		}
		c.output(t, "COPY (SELECT v FROM copy_csv_header_one) TO STDOUT WITH(FORMAT csv,HEADER true)", "v\n", 0)
		c.exec(t, "CREATE TABLE copy_csv_names(\"x,y\" text,\"a\"\"b\" text,\"line\nbreak\" int)")
		result, err = c.from("COPY copy_csv_names FROM STDIN WITH(FORMAT csv,HEADER MATCH)", strings.NewReader("\"x,y\",\"a\"\"b\",\"line\nbreak\"\nleft,right,7\n"))
		copyTextSuccess(t, result, err, 1)
		c.output(t, "COPY (SELECT * FROM copy_csv_names) TO STDOUT WITH(FORMAT csv,HEADER true)", "\"x,y\",\"a\"\"b\",\"line\nbreak\"\nleft,right,7\n", 1)
		result, err = c.from("COPY copy_csv_names FROM STDIN WITH(FORMAT csv,HEADER MATCH)", strings.NewReader("\"x,y\",\"a\"\"b\",\"line\nbreak\"\nleft,right,bad\n"))
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type integer: \"bad\"", Context: "COPY copy_csv_names, line 2, column line\nbreak: \"bad\""})
		c.output(t, "COPY (SELECT a AS \"x,y\",b AS \"a\"\"b\" FROM copy_csv_header) TO STDOUT WITH(FORMAT csv,HEADER true)", "\"x,y\",\"a\"\"b\"\ny,x\n", 1)
	})
}

func TestReferenceCopyCSVForceOptions(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_csv_force(id int,a text,b text)")
		for _, marker := range []string{"", "NIL"} {
			for _, tc := range []struct {
				option           string
				unquoted, quoted any
			}{
				{"", nil, ""}, {",FORCE_NOT_NULL(a)", "", ""}, {",FORCE_NULL(a)", nil, nil}, {",FORCE_NOT_NULL(a),FORCE_NULL(a)", "", nil},
			} {
				unquoted, quoted := tc.unquoted, tc.quoted
				if marker != "" {
					if unquoted != nil {
						unquoted = marker
					}
					if quoted != nil {
						quoted = marker
					}
				}
				input := marker + ",1," + marker + "\n\"" + marker + "\",2,\"" + marker + "\"\n"
				result, err := c.from("COPY copy_csv_force(b,id,a) FROM STDIN WITH(FORMAT csv,NULL '"+marker+"'"+tc.option+")", strings.NewReader(input))
				copyTextSuccess(t, result, err, 2)
				c.selectRows(t, "SELECT * FROM copy_csv_force ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), unquoted, nil}, {int64(2), quoted, marker}})
				c.exec(t, "DELETE FROM copy_csv_force")
			}
		}
		for _, option := range []string{"FORCE_NULL *", "FORCE_NOT_NULL *", "FORCE_NULL *,FORCE_NOT_NULL *"} {
			result, err := c.from("COPY copy_csv_force(a,b) FROM STDIN WITH(FORMAT csv,"+option+")", strings.NewReader(",\"\"\n"))
			copyTextSuccess(t, result, err, 1)
			var a, b any
			if strings.Contains(option, "FORCE_NOT_NULL") {
				a = ""
				if !strings.Contains(option, "FORCE_NULL") {
					b = ""
				}
			}
			c.selectRows(t, "SELECT a,b FROM copy_csv_force", []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{a, b}})
			c.exec(t, "DELETE FROM copy_csv_force")
		}
		result, err := c.from("COPY copy_csv_force(id) FROM STDIN WITH(FORMAT csv,FORCE_NOT_NULL(id))", strings.NewReader("\n"))
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type integer: \"\"", Context: "COPY copy_csv_force, line 1, column id: \"\""})
		c.exec(t, "INSERT INTO copy_csv_force VALUES(1,'plain','neighbor'),(2,NULL,'neighbor')")
		c.output(t, "COPY (SELECT * FROM copy_csv_force ORDER BY id) TO STDOUT WITH(FORMAT csv,HEADER true,FORCE_QUOTE(a))", "id,a,b\n1,\"plain\",neighbor\n2,,neighbor\n", 2)
		c.output(t, "COPY (SELECT * FROM copy_csv_force ORDER BY id) TO STDOUT WITH(FORMAT csv,FORCE_QUOTE *)", "\"1\",\"plain\",\"neighbor\"\n\"2\",,\"neighbor\"\n", 2)
		c.output(t, "COPY (SELECT a AS alias,b FROM copy_csv_force WHERE id=1) TO STDOUT WITH(FORMAT csv,FORCE_QUOTE(alias))", "\"plain\",neighbor\n", 1)
	})
}

func TestReferenceCopyCSVOptionsDiagnostics(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_csv_options(v text,w text,g int GENERATED ALWAYS AS(1) STORED)")
		for _, tc := range []struct{ option, dir, code, message string }{
			{"FORMAT csv,QUOTE 'é'", "FROM", "0A000", "COPY quote must be a single one-byte character"}, {"FORMAT csv,ESCAPE 'é'", "FROM", "0A000", "COPY escape must be a single one-byte character"},
			{"QUOTE '\"'", "FROM", "0A000", "COPY QUOTE requires CSV mode"}, {"ESCAPE '!'", "FROM", "0A000", "COPY ESCAPE requires CSV mode"},
			{"FORCE_QUOTE *", "TO", "0A000", "COPY FORCE_QUOTE requires CSV mode"}, {"FORCE_NULL(v)", "FROM", "0A000", "COPY FORCE_NULL requires CSV mode"}, {"FORCE_NOT_NULL(v)", "FROM", "0A000", "COPY FORCE_NOT_NULL requires CSV mode"},
			{"FORMAT csv,FORCE_QUOTE *", "FROM", "0A000", "COPY FORCE_QUOTE cannot be used with COPY FROM"}, {"FORMAT csv,FORCE_NULL(v)", "TO", "22023", "COPY FORCE_NULL cannot be used with COPY TO"}, {"FORMAT csv,FORCE_NOT_NULL(v)", "TO", "22023", "COPY FORCE_NOT_NULL cannot be used with COPY TO"},
			{"FORMAT csv,DELIMITER '\"'", "FROM", "22023", "COPY delimiter and quote must be different"}, {"FORMAT csv,NULL 'a\"b'", "FROM", "22023", "CSV quote character must not appear in the NULL specification"}, {"FORMAT csv,DEFAULT 'a\"b'", "FROM", "0A000", "CSV quote character must not appear in the DEFAULT specification"},
			{"FORMAT csv,HEADER MATCH", "TO", "0A000", "cannot use \"match\" with HEADER in COPY TO"},
		} {
			t.Run(tc.option+tc.dir, func(t *testing.T) {
				q := "COPY copy_csv_options(v) " + tc.dir
				var result copyTextResult
				var err error
				never := &copyTextNoRead{}
				var output bytes.Buffer
				if tc.dir == "FROM" {
					q += " STDIN WITH(" + tc.option + ")"
					result, err = c.from(q, never)
				} else {
					q += " STDOUT WITH(" + tc.option + ")"
					result, err = c.to(q, &output)
				}
				copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: tc.code, Message: tc.message})
				if !b.PG && never.calls != 0 {
					t.Fatal("preflight read caller")
				}
				if output.Len() != 0 {
					t.Fatal("preflight wrote caller")
				}
			})
		}
		for _, force := range []string{"force_quote", "force_null", "force_not_null"} {
			dir, stream := "FROM", "STDIN"
			if force == "force_quote" {
				dir, stream = "TO", "STDOUT"
			}
			run := func(q string, want fixture.Diagnostic) {
				never := &copyTextNoRead{}
				var out bytes.Buffer
				var result copyTextResult
				var err error
				if dir == "FROM" {
					result, err = c.from(q, never)
				} else {
					result, err = c.to(q, &out)
				}
				copyTextDiagnostic(t, result, err, want)
				if !b.PG && never.calls != 0 || out.Len() != 0 {
					t.Fatal("invalid force touched stream")
				}
			}
			for _, shape := range []string{"'v'", "'*'", ""} {
				q := "COPY copy_csv_options(v) " + dir + " " + stream + " WITH(FORMAT csv," + force + " " + shape + ")"
				run(q, fixture.Diagnostic{Code: "22023", Message: fmt.Sprintf("argument to option %q must be a list of column names", force), Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, force)]) + 1)})
			}
			for _, tc := range []struct{ names, code, message, detail string }{
				{"missing", "42703", "column \"missing\" of relation \"copy_csv_options\" does not exist", ""}, {"v,v", "42701", "column \"v\" specified more than once", ""}, {"w", "42P10", strings.ToUpper(force) + " column \"w\" not referenced by COPY", ""}, {"g", "42P10", "column \"g\" is a generated column", "Generated columns cannot be used in COPY."},
			} {
				q := "COPY copy_csv_options(v) " + dir + " " + stream + " WITH(FORMAT csv," + force + "(" + tc.names + "))"
				run(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail})
			}
			q := "COPY copy_csv_options(v) " + dir + " " + stream + " WITH(FORMAT csv," + force + "(v)," + force + " *)"
			run(q, fixture.Diagnostic{Code: "42601", Message: "conflicting or redundant options", Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.LastIndex(q, force)]) + 1)})
		}
		result, err := c.to("COPY (SELECT v AS alias FROM copy_csv_options) TO STDOUT WITH(FORMAT csv,FORCE_QUOTE(missing))", &bytes.Buffer{})
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "42703", Message: "column \"missing\" does not exist"})
	})
}

// Accepted pathological one-byte settings must not acquire invented validation bans.
func TestReferenceCopyCSVPathologicalOptions(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_csv_path(v text)")
		c.exec(t, "INSERT INTO copy_csv_path VALUES('plain')")
		for _, opts := range []string{"QUOTE E'\\n'", "QUOTE E'\\r'", "ESCAPE E'\\n'", "ESCAPE E'\\r'", "ESCAPE ','"} {
			c.output(t, "COPY copy_csv_path TO STDOUT WITH(FORMAT csv,"+opts+")", "plain\n", 1)
			result, err := c.from("COPY copy_csv_path FROM STDIN WITH(FORMAT csv,"+opts+")", strings.NewReader("safe"))
			copyTextSuccess(t, result, err, 1)
			c.selectRows(t, "SELECT v FROM copy_csv_path ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"plain"}, {"safe"}})
			c.exec(t, "DELETE FROM copy_csv_path WHERE v='safe'")
		}
		// ESCAPE=delimiter while quoted: escaped comma and quote are decoded only inside quotes.
		result, err := c.from("COPY copy_csv_path FROM STDIN WITH(FORMAT csv,ESCAPE ',')", &copyTextChunks{text: "\"a,,b,\"c\"\n", widths: []int{1}})
		copyTextSuccess(t, result, err, 1)
		c.selectRows(t, "SELECT v FROM copy_csv_path ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"a,b\"c"}, {"plain"}})
		c.output(t, "COPY (SELECT v FROM copy_csv_path WHERE v='a,b\"c') TO STDOUT WITH(FORMAT csv,ESCAPE ',')", "\"a,,b,\"c\"\n", 1)
	})
}
