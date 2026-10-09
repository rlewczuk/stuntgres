package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestJSONTypedLiteralSyntax(t *testing.T) {
	for _, q := range []string{`SELECT json 'null' AS v`, `SELECT pg_catalog.json 'null' AS v`, `SELECT "json" 'null' AS v`, `SELECT CAST('null' AS json) AS v`, `SELECT 'null'::json AS v`, `SELECT CAST(json 'null' AS text) AS v`} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, err)
		}
		selected := statements[0].(*Select)
		if !selected.IsTypeSingleton() {
			t.Fatal("missing bounded syntax candidate", q)
		}
	}
	for _, q := range []string{`SELECT json 'null' WHERE TRUE`, `SELECT json 'null' ORDER BY 1`, `SELECT json 'null' GROUP BY 1`, `SELECT json 'null' FOR UPDATE`} {
		statements, err := Parse(q)
		if err == nil && statements[0].(*Select).IsTypeSingleton() {
			t.Fatal("unbounded candidate", q)
		}
	}
	_, err := Parse("CREATE TABLE m(v json(2))")
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "("`, Position: "22"}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("got %+v (%v) want %+v", got, err, want)
	}
}
