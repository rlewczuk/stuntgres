package reference_test

import (
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

const copyCSVGolden = "1,,\"\"\n2,\"x,y\",\"a\"\"b\"\n3,\"line\nbreak\",\"cr\r\nlf\"\n"

func TestReferenceCopyCSVRoundTrip(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_csv_round(id int,a text,b text)")
		stmt, err := c.conn.PrepareContext(c.ctx, "SELECT * FROM copy_csv_round ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		result, err := c.from("COPY copy_csv_round FROM STDIN WITH(FORMAT csv)", strings.NewReader(copyCSVGolden))
		copyTextSuccess(t, result, err, 3)
		want := [][]any{{int64(1), nil, ""}, {int64(2), "x,y", "a\"b"}, {int64(3), "line\nbreak", "cr\r\nlf"}}
		c.selectRows(t, "SELECT * FROM copy_csv_round ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "TEXT", "TEXT"}, want)
		rows, err := stmt.QueryContext(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		names, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(names, []string{"id", "a", "b"}) {
			t.Fatal(names, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, name := range []string{"INT4", "TEXT", "TEXT"} {
			if types[i].DatabaseTypeName() != name {
				t.Fatal(types[i])
			}
		}
		var got [][]any
		for rows.Next() {
			var id int64
			var a, b any
			if err = rows.Scan(&id, &a, &b); err != nil {
				t.Fatal(err)
			}
			got = append(got, []any{id, fixture.Normalize(a), fixture.Normalize(b)})
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !reflect.DeepEqual(got, want) {
			t.Fatal(got)
		}
		c.output(t, "COPY (SELECT * FROM copy_csv_round ORDER BY id) TO STDOUT WITH(FORMAT csv)", copyCSVGolden, 3)
		for _, prepared := range []bool{false, true} {
			if prepared {
				insert, err := c.conn.PrepareContext(c.ctx, "INSERT INTO copy_csv_round VALUES($1,$2,$3)")
				if err != nil {
					t.Fatal(err)
				}
				result, err := insert.ExecContext(c.ctx, 5, "quote\"\n", "plain")
				insert.Close()
				if err != nil {
					t.Fatal(err)
				}
				n, err := result.RowsAffected()
				if err != nil || n != 1 {
					t.Fatal(n, err)
				}
			} else {
				result, err := c.conn.ExecContext(c.ctx, "INSERT INTO copy_csv_round VALUES($1,$2,$3)", 4, "quote\"\n", "plain")
				if err != nil {
					t.Fatal(err)
				}
				n, err := result.RowsAffected()
				if err != nil || n != 1 {
					t.Fatal(n, err)
				}
			}
		}
		c.output(t, "COPY (SELECT a,b FROM copy_csv_round WHERE id=4) TO STDOUT WITH(FORMAT csv)", "\"quote\"\"\n\",plain\n", 1)
		result, err = c.from("COPY copy_csv_round FROM STDIN WITH(FORMAT csv)", strings.NewReader("6,good,ok\n7,\"unfinished"))
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22P04", Message: "unterminated CSV quoted field", Context: "COPY copy_csv_round, line 2: \"7,\"unfinished\""})
		rows, err = stmt.QueryContext(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			count++
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if count != 5 {
			t.Fatal(count)
		}
	})
}

func TestReferenceCopyCSVFraming(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		for _, tc := range []struct {
			name, input string
			want        [][]any
		}{
			{"empty", "", [][]any{}}, {"newline", "\n", [][]any{{nil}}}, {"eof", "final", [][]any{{"final"}}},
			{"lf", "\"a\nb\"\nc\n", [][]any{{"a\nb"}, {"c"}}}, {"cr", "\"a\rb\"\rc\r", [][]any{{"a\rb"}, {"c"}}}, {"crlf", "\"a\r\nb\"\r\nc\r\n", [][]any{{"a\r\nb"}, {"c"}}},
			{"utf8", "\"é漢,\"\"x\"\n", [][]any{{"é漢,\"x"}}}, {"large", "\"" + strings.Repeat("é", 40000) + ",end\"\n", [][]any{{strings.Repeat("é", 40000) + ",end"}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, widths := range [][]int{{1}, {3, 1, 2, 7, 5}} {
					c.exec(t, "CREATE TABLE copy_csv_frame(v text)")
					dropped := false
					t.Cleanup(func() {
						if !dropped {
							c.exec(t, "DROP TABLE IF EXISTS copy_csv_frame")
						}
					})
					result, err := c.from("COPY copy_csv_frame FROM STDIN WITH(FORMAT csv)", &copyTextChunks{text: tc.input, widths: widths})
					copyTextSuccess(t, result, err, int64(len(tc.want)))
					c.selectRows(t, "SELECT v FROM copy_csv_frame ORDER BY v", []string{"v"}, []string{"TEXT"}, tc.want)
					c.exec(t, "DROP TABLE copy_csv_frame")
					dropped = true
				}
			})
		}
	})
}

func TestReferenceCopyCSVEscapes(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_csv_escape(id int,v text)")
		result, err := c.from("COPY copy_csv_escape FROM STDIN WITH(FORMAT csv)", strings.NewReader("1,a\"b\"c\n2,\"a\" b\n3,  x  \n4,\\n\\N\n"))
		copyTextSuccess(t, result, err, 4)
		c.selectRows(t, "SELECT * FROM copy_csv_escape ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "abc"}, {int64(2), "a b"}, {int64(3), "  x  "}, {int64(4), "\\n\\N"}})
		c.exec(t, "CREATE TABLE copy_csv_custom(v text)")
		for _, opts := range []string{"FORMAT csv,QUOTE '''',ESCAPE '!'", "ESCAPE '!',QUOTE '''',FORMAT csv", "QUOTE '''',FORMAT csv,ESCAPE '!'"} {
			result, err = c.from("COPY copy_csv_custom FROM STDIN WITH("+opts+")", &copyTextChunks{text: "'a!'b!!c'\n'!x'\na!b\n", widths: []int{1, 2, 3}})
			copyTextSuccess(t, result, err, 3)
			c.selectRows(t, "SELECT v FROM copy_csv_custom ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"!x"}, {"a!b"}, {"a'b!c"}})
			c.output(t, "COPY (SELECT v FROM copy_csv_custom ORDER BY v) TO STDOUT WITH("+opts+")", "!x\na!b\n'a!'b!!c'\n", 3)
			c.exec(t, "DELETE FROM copy_csv_custom")
		}
		result, err = c.from("COPY copy_csv_custom FROM STDIN WITH(FORMAT csv)", strings.NewReader("\\.\ntail\n"))
		copyTextSuccess(t, result, err, 2)
		c.output(t, "COPY (SELECT v FROM copy_csv_custom ORDER BY v) TO STDOUT WITH(FORMAT csv)", "\"\\.\"\ntail\n", 2)
		c.output(t, "COPY (SELECT id,v FROM copy_csv_escape WHERE id=4) TO STDOUT WITH(FORMAT csv)", "4,\\n\\N\n", 1)
		for _, delim := range []string{"a", "\\", ".", "1"} {
			c.exec(t, "CREATE TABLE copy_csv_delim(x text,y text)")
			result, err = c.from("COPY copy_csv_delim FROM STDIN WITH(DELIMITER '"+delim+"',FORMAT csv)", strings.NewReader("x"+delim+"y\n"))
			copyTextSuccess(t, result, err, 1)
			c.output(t, "COPY copy_csv_delim TO STDOUT WITH(DELIMITER '"+delim+"',FORMAT csv)", "x"+delim+"y\n", 1)
			c.exec(t, "DROP TABLE copy_csv_delim")
		}
	})
}
