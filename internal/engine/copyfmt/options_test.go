package copyfmt

import (
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// copyStatement parses one COPY statement.
func copyStatement(t *testing.T, q string) *parser.Copy {
	t.Helper()
	statements, err := parser.Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	return statements[0].(*parser.Copy)
}

// csvOptions parses the options of one COPY statement that must be valid.
func csvOptions(t *testing.T, q string) Options {
	t.Helper()
	o, err := ParseOptions(copyStatement(t, q))
	if err != nil {
		t.Fatal(err)
	}
	return o
}
func TestCopyCSVOptions(t *testing.T) {
	for _, q := range []string{"COPY t FROM STDIN WITH(FORMAT csv)", "COPY t FROM STDIN WITH(QUOTE '''',FORMAT csv)", "COPY t FROM STDIN WITH(ESCAPE '!',QUOTE '''',FORMAT csv)"} {
		o := csvOptions(t, q)
		if o.Format != CSV || o.Delimiter != ',' || o.Null != "" {
			t.Fatal(o)
		}
		quote, escape := byte('"'), byte('"')
		if strings.Contains(q, "QUOTE") {
			quote = '\''
			escape = quote
		}
		if strings.Contains(q, "ESCAPE") {
			escape = '!'
		}
		if o.Quote != quote || o.Escape != escape {
			t.Fatal(o)
		}
	}
	o := csvOptions(t, "COPY t FROM STDIN WITH(FORMAT text)")
	if o.Format == CSV || o.Delimiter != '\t' || o.Null != `\N` {
		t.Fatal(o)
	}
}
func TestCopyBinaryOptions(t *testing.T) {
	for _, q := range []string{"COPY t FROM STDIN WITH(FORMAT binary)", "COPY t TO STDOUT WITH(FORMAT binary,HEADER false)"} {
		o, err := ParseOptions(copyStatement(t, q))
		if err != nil || o.Format != Binary {
			t.Fatal(o, err)
		}
	}
	for _, tc := range []struct{ q, code, message string }{{"COPY t FROM STDIN WITH(FORMAT binary,HEADER true)", "0A000", "cannot specify HEADER in BINARY mode"}, {"COPY t FROM STDIN WITH(FORMAT binary,NULL '')", "42601", "cannot specify NULL in BINARY mode"}, {"COPY t TO STDOUT WITH(FORMAT binary,QUOTE 'x')", "0A000", "COPY QUOTE requires CSV mode"}} {
		_, err := ParseOptions(copyStatement(t, tc.q))
		var d *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !errors.As(err, &d) || *d != want {
			t.Fatal(tc.q, err)
		}
	}
}
