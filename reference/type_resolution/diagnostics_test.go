package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceErrorFields(t *testing.T) {
	p := fixture.NewScenario(t)
	syntax := &fixture.Diagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601",
		Message: `syntax error at or near ")"`, Position: "28",
	}
	for _, step := range []fixture.Step{
		{Query: "INSERT INTO t VALUES ('é', )", State: "42601", Diagnostic: syntax},
		{Query: "INSERT INTO t VALUES ('é', )", State: "42601", Diagnostic: syntax, SelectRows: true},
		{Query: "INSERT INTO t VALUES ('é', )", State: "42601", Diagnostic: syntax, Prepared: true},
	} {
		p.Step(t, step)
	}
	for _, tc := range []struct {
		query, message, position string
	}{
		{"INSERT INTO t VALUES ('😀', )", `syntax error at or near ")"`, "28"},
		{"CREATE TABLE t (x INT", "syntax error at end of input", "22"},
		{"CREATE TABLE t (x INT, \"Bad\")", `syntax error at or near ")"`, "29"},
		{"CREATE TABLE t (x INT) GARBAGE", `syntax error at or near "GARBAGE"`, "24"},
		{"/*é*/ /* never closed", `unterminated /* comment at or near "/* never closed"`, "7"},
		{"INSERT INTO t VALUES ('é", `unterminated quoted string at or near "'é"`, "23"},
		{`SELECT * FROM "Bad`, `unterminated quoted identifier at or near ""Bad"`, "15"},
		{"INSERT INTO t VALUES ('first'\n 'later", "unterminated quoted string at or near \"'first'\n 'later\"", "23"},
		{`SELECT * FROM "" trailing`, `zero-length delimited identifier at or near """"`, "15"},
	} {
		want := fixture.Diagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601",
			Message: tc.message, Position: tc.position,
		}
		p.Step(t, fixture.ErrorDiagnostic(tc.query, want))
		p.Step(t, fixture.Prepared(fixture.ErrorDiagnostic(tc.query, want)))
	}
	p.Step(t, fixture.Error("CREATE TABLE syntax_prefix (x INT); )", "42601"))
	s := fixture.Error("SELECT * FROM syntax_prefix", "42P01")
	s.SelectRows = true
	p.Step(t, s)
	p.Step(t, fixture.Exec("CREATE TABLE row_formats (id INT NOT NULL,big BIGINT,flag BOOL,note TEXT)", 0))
	for _, tc := range []struct {
		name, big, flag, note, detail string
	}{
		{"values", "-9223372036854775808", "TRUE", "a,b\nc", "Failing row contains (null, -9223372036854775808, t, a,b\nc)."},
		{"false", "9223372036854775807", "FALSE", "edge", "Failing row contains (null, 9223372036854775807, f, edge)."},
		{"exact_64", "NULL", "NULL", strings.Repeat("x", 64), "Failing row contains (null, null, null, " + strings.Repeat("x", 64) + ")."},
		{"long_ascii", "NULL", "NULL", strings.Repeat("x", 65), "Failing row contains (null, null, null, " + strings.Repeat("x", 64) + "...)."},
		{"long_utf8", "NULL", "NULL", strings.Repeat("x", 63) + "é", "Failing row contains (null, null, null, " + strings.Repeat("x", 63) + "...)."},
	} {
		message := `null value in column "id" of relation "row_formats" violates not-null constraint`
		expected := &fixture.Diagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: message,
			Detail: tc.detail, Schema: "public", Table: "row_formats", Column: "id",
		}
		query := fmt.Sprintf("INSERT INTO row_formats VALUES (NULL,%s,%s,'%s')", tc.big, tc.flag, tc.note)
		p.Step(t, fixture.Step{Query: query, State: "23502", Diagnostic: expected})
	}
}

func TestReferenceErrorFieldsConversion(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE diagnostics (id INT, flag BOOL)", 0))
	for _, tc := range []struct {
		query, value, code, message string
	}{
		{"INSERT INTO diagnostics VALUES ($1,TRUE)", "not an integer", "22P02", `invalid input syntax for type integer: "not an integer"`},
		{"SELECT id FROM diagnostics WHERE id=$1", "not an integer", "22P02", `invalid input syntax for type integer: "not an integer"`},
		{"INSERT INTO diagnostics (flag,id) VALUES ($1,$2)", "ambiguous", "22P02", `invalid input syntax for type boolean: "ambiguous"`},
		{"SELECT id FROM diagnostics WHERE id=$1", "2147483648", "22003", `value "2147483648" is out of range for type integer`},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%t/%s/%s", prepared, tc.code, tc.query), func(t *testing.T) {
				native := fixture.Diagnostic{
					Severity: "ERROR", SeverityUnlocalized: "ERROR",
					Code: tc.code, Message: tc.message,
				}
				native.Context = "unnamed portal parameter $1 = '...'"
				args := []any{tc.value}
				if strings.HasPrefix(tc.query, "INSERT INTO diagnostics (flag,id)") {
					args = append(args, 1)
				}
				p.Step(t, fixture.Step{
					Query: tc.query, Args: args, Prepared: prepared,
					SelectRows: strings.HasPrefix(tc.query, "SELECT "),
					State:      tc.code, Diagnostic: &native, ErrorStage: "execute",
				})
			})
		}
	}
	p.Step(t, fixture.Select("SELECT id FROM diagnostics ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
}
