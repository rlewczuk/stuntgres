package parser

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestCTEParserPositionedDefinitions(t *testing.T) {
	const text = `/* é */ WITH "C"(k) AS MATERIALIZED (SELECT 1 AS x), b AS NOT MATERIALIZED (SELECT k FROM "C") SELECT * FROM b`
	statements, err := Parse(text)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	query := statements[0].(*Select)
	if len(query.With) != 2 {
		t.Fatal(query.With)
	}
	first, second := query.With[0], query.With[1]
	if first.Name.Text != "C" || len(first.Columns) != 1 || first.Columns[0].Text != "k" || first.Materialization != CTEMaterialized || second.Materialization != CTENotMaterialized {
		t.Fatal(query.With)
	}
	at := strings.Index(text, `"C"`)
	if first.Name.Pos.Offset != at || first.Name.Pos.Character != utf8.RuneCountInString(text[:at])+1 {
		t.Fatal(first.Name.Pos)
	}
	if first.Query == nil || second.Query == nil || first.Query == second.Query {
		t.Fatal("definition bodies lost")
	}
}

func TestCTEParserSelectConsumers(t *testing.T) {
	const query = "WITH c AS (SELECT 1 AS n) SELECT n FROM c"
	for _, text := range []string{
		query,
		"SELECT (" + query + ")",
		"SELECT * FROM (" + query + ") AS d",
		"(" + query + ") UNION SELECT 2",
		"CREATE VIEW v AS " + query,
		"CREATE MATERIALIZED VIEW v AS " + query + " WITH NO DATA",
		"CREATE TABLE t AS " + query,
		"WITH c AS (SELECT 1 AS n) SELECT n INTO t FROM c",
		"INSERT INTO t " + query,
		"DECLARE c CURSOR FOR " + query,
		"PREPARE p AS " + query,
	} {
		t.Run(text, func(t *testing.T) {
			statements, err := Parse(text)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
		})
	}
	statements, err := Parse(query + "; nonsense")
	if err == nil || len(statements) != 0 {
		t.Fatal("accepted prefix of malformed whole input", statements, err)
	}
}

func TestCTEParserLexicalSources(t *testing.T) {
	type classified struct {
		name    string
		virtual bool
		future  string
	}
	name := func(object ObjectName) string {
		parts := make([]string, len(object.Parts))
		for i, part := range object.Parts {
			parts[i] = part.Text
		}
		return strings.Join(parts, ".")
	}
	for _, tc := range []struct {
		text     string
		physical []string
		all      []classified
	}{
		{"WITH a AS (SELECT * FROM p), b AS (SELECT * FROM a) SELECT * FROM b JOIN public.a ON TRUE", []string{"p", "public.a"}, []classified{{"p", false, ""}, {"a", true, ""}, {"b", true, ""}, {"public.a", false, ""}}},
		{"WITH a AS (SELECT * FROM b), b AS (SELECT 1) SELECT * FROM a", []string{"b"}, []classified{{"b", false, "b"}, {"a", true, ""}}},
		{"WITH a AS (SELECT 1) SELECT (WITH a AS (SELECT * FROM a), b AS (SELECT * FROM later), later AS (SELECT 1) SELECT * FROM b)", []string{"later"}, []classified{{"a", true, ""}, {"later", false, "later"}, {"b", true, ""}}},
	} {
		t.Run(tc.text, func(t *testing.T) {
			statements, err := Parse(tc.text)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				var physical []string
				var all []classified
				err = VisitQuerySources(statements[0], nil, func(table *TableReference, _ *Select) error {
					physical = append(physical, name(table.Name))
					return nil
				}, func(table *TableReference, virtual bool, future string) {
					all = append(all, classified{name(table.Name), virtual, future})
				})
				if err != nil || !reflect.DeepEqual(physical, tc.physical) || !reflect.DeepEqual(all, tc.all) {
					t.Fatal(physical, all, err)
				}
			}
		})
	}
}

func TestCTEParserDuplicateBeforeBodyAdmission(t *testing.T) {
	statements, err := Parse("WITH c AS (SELECT * FROM missing), c AS (SELECT 2) SELECT 7")
	var diagnostic *sqlstate.Error
	if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42712" || diagnostic.Message != `WITH query name "c" specified more than once` || diagnostic.Position != "36" {
		t.Fatal(statements, err)
	}
	// Parse's complete-input prewalk already rejects the duplicate. Exercise
	// source admission independently using the positioned AST, not a prefix.
	first := &Select{From: &TableReference{Name: ObjectName{Parts: []Identifier{{Text: "missing"}}}}}
	query := &Select{With: []CommonTableExpression{
		{Name: Identifier{Text: "c", Pos: Position{Offset: 5, Character: 6}}, Query: first},
		{Name: Identifier{Text: "c", Pos: Position{Offset: 35, Character: 36}}, Query: &Select{}},
	}}
	visited := 0
	err = VisitQuerySources(query, func(*Select) error { visited++; return nil }, func(*TableReference, *Select) error { visited++; return nil })
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42712" || diagnostic.Message != `WITH query name "c" specified more than once` || diagnostic.Position != "36" || visited != 0 {
		t.Fatal(visited, err)
	}
}

func TestCTEParserRepeatedWithPosition(t *testing.T) {
	for _, prefix := range []string{"", "/* é */ "} {
		statements, err := Parse(prefix + "WITH c AS (SELECT 1) (WITH c AS (SELECT 2) SELECT * FROM c)")
		var diagnostic *sqlstate.Error
		want := "1"
		if prefix != "" {
			want = "9"
		}
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "multiple WITH clauses not allowed" || diagnostic.Position != want {
			t.Fatal(statements, err)
		}
	}
}
