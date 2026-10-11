package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCopyTextDiagnostics(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_errors(id int,a text,b text)")
		c.exec(t, "INSERT INTO copy_errors VALUES(99,'sentinel','')")
		for _, tc := range []struct {
			name, input string
			want        fixture.Diagnostic
		}{
			{"excess", "1\tok\tgood\n2\tx\ty\tz\n", fixture.Diagnostic{Code: "22P04", Message: "extra data after last expected column", Context: "COPY copy_errors, line 2: \"2\tx\ty\tz\""}},
			{"missing", "1\tok\tgood\n2\tx\n", fixture.Diagnostic{Code: "22P04", Message: `missing data for column "b"`, Context: "COPY copy_errors, line 2: \"2\tx\""}},
			{"conversion", "1\tok\tgood\nbad\tx\ty\n", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: `COPY copy_errors, line 2, column id: "bad"`}},
			{"range", "1\tok\tgood\n2147483648\tx\ty\n", fixture.Diagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Context: `COPY copy_errors, line 2, column id: "2147483648"`}},
			{"excess_before_conversion", "1\tok\tgood\nbad\tx\ty\tz\n", fixture.Diagnostic{Code: "22P04", Message: "extra data after last expected column", Context: "COPY copy_errors, line 2: \"bad\tx\ty\tz\""}},
			{"conversion_before_missing", "1\tok\tgood\nbad\tx\n", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: `COPY copy_errors, line 2, column id: "bad"`}},
			{"nul", "1\tok\tgood\n2\t\\000\ty\n", fixture.Diagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0x00`, Context: "COPY copy_errors, line 2: \"2\t\\000\ty\""}},
			{"utf8", "1\tok\tgood\n2\t\xff\ty\n", fixture.Diagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0xff`, Context: "COPY copy_errors, line 2"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				result, err := c.from("COPY copy_errors FROM STDIN", strings.NewReader(tc.input))
				copyTextDiagnostic(t, result, err, tc.want)
				c.selectRows(t, "SELECT * FROM copy_errors ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(99), "sentinel", ""}})
			})
		}
		for _, tc := range []struct {
			name, ddl, input string
			want             fixture.Diagnostic
		}{
			{"not_null", "CREATE TABLE copy_nn(id int,v text NOT NULL)", "1\tok\n2\t\\N\n", fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "copy_nn" violates not-null constraint`, Detail: "Failing row contains (2, null).", Schema: "public", Table: "copy_nn", Column: "v", Context: "COPY copy_nn, line 2: \"2\t\\N\""}},
			{"check", "CREATE TABLE copy_check(id int CHECK(id>0))", "1\n-1\n", fixture.Diagnostic{Code: "23514", Message: `new row for relation "copy_check" violates check constraint "copy_check_id_check"`, Detail: "Failing row contains (-1).", Schema: "public", Table: "copy_check", Constraint: "copy_check_id_check", Context: `COPY copy_check, line 2: "-1"`}},
			{"unique", "CREATE TABLE copy_unique(id int PRIMARY KEY)", "1\n1\n", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "copy_unique_pkey"`, Detail: "Key (id)=(1) already exists.", Schema: "public", Table: "copy_unique", Constraint: "copy_unique_pkey", Context: `COPY copy_unique, line 2`}},
			{"default_expression", "CREATE TABLE copy_bad_default(id int DEFAULT (1/0))", "DEF\n", fixture.Diagnostic{Code: "22012", Message: "division by zero"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				c.exec(t, tc.ddl)
				q := "COPY " + map[string]string{"not_null": "copy_nn", "check": "copy_check", "unique": "copy_unique", "default_expression": "copy_bad_default"}[tc.name] + " FROM STDIN"
				if tc.name == "default_expression" {
					q += " WITH(DEFAULT 'DEF')"
				}
				result, err := c.from(q, strings.NewReader(tc.input))
				copyTextDiagnostic(t, result, err, tc.want)
			})
		}
	})
}
