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
		if selected.From != nil || len(selected.Targets) != 1 {
			t.Fatal("no-FROM JSON expression lost its target", q)
		}
	}
	for _, q := range []string{`SELECT json 'null' WHERE TRUE`, `SELECT json 'null' ORDER BY 1`, `SELECT json 'null' GROUP BY 1`, `SELECT json 'null' FOR UPDATE`} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		selected := statements[0].(*Select)
		if selected.From != nil || len(selected.Targets) != 1 {
			t.Fatal("ordinary no-FROM JSON expression lost its target", q)
		}
		switch q {
		case `SELECT json 'null' WHERE TRUE`:
			if selected.Where == nil {
				t.Fatal("missing WHERE", q)
			}
		case `SELECT json 'null' ORDER BY 1`:
			if selected.Order == nil {
				t.Fatal("missing ORDER BY", q)
			}
		case `SELECT json 'null' GROUP BY 1`:
			if selected.GroupBy == nil || len(selected.GroupBy.Items) != 1 {
				t.Fatal("missing GROUP BY", q)
			}
		case `SELECT json 'null' FOR UPDATE`:
			if selected.Locking == nil {
				t.Fatal("missing row mark", q)
			}
		}
	}
	_, err := Parse("CREATE TABLE m(v json(2))")
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "("`, Position: "22"}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("got %+v (%v) want %+v", got, err, want)
	}
}

func TestJSONBTypedLiteralSyntax(t *testing.T) {
	for _, q := range []string{`SELECT jsonb 'null' AS v`, `SELECT CAST('null' AS pg_catalog.jsonb) AS v`, `SELECT "jsonb" 'null' AS v`, `SELECT CAST('null' AS jsonb) AS v`, `SELECT 'null'::jsonb AS v`} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, err)
		}
		selected := statements[0].(*Select)
		if selected.From != nil || len(selected.Targets) != 1 {
			t.Fatal("JSONB target lost", q)
		}
	}
	if lookupKeyword("jsonb") != nil {
		t.Fatal("JSONB became reserved")
	}
}
