package parser

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestQueryLateralParser(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		values       bool
	}{
		{"select", "(SELECT t.v AS n)", false},
		{"nested", "(((SELECT t.v AS n)))", false},
		{"with", "(WITH q AS (SELECT t.v AS n) SELECT n FROM q)", false},
		{"with_values", "(WITH q AS (SELECT t.v AS n) VALUES ((SELECT n FROM q)))", false},
		{"set", "(SELECT t.v AS n UNION ALL SELECT t.v)", false},
		{"bounds", "(SELECT t.v AS n ORDER BY n LIMIT 1)", false},
		{"values", "(VALUES (t.v, 1), (t.v, 2) ORDER BY 2 LIMIT 1)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, prefix := range []string{"", "LATERAL "} {
				sql := "SELECT * FROM t CROSS JOIN " + prefix + tc.source + " AS x(n)"
				statements, err := Parse(sql)
				if err != nil || len(statements) != 1 {
					t.Fatal(statements, err)
				}
				join := statements[0].(*Select).From.(*JoinReference)
				alias := join.Right.(*AliasReference)
				if alias.Name.Text != "x" || len(alias.Columns) != 1 || alias.Columns[0].Text != "n" {
					t.Fatal("source alias boundary lost", alias)
				}
				lateral := prefix != ""
				if tc.values {
					n, ok := alias.Input.(*ValuesQuery)
					if !ok || n.Lateral != lateral || len(n.Rows) != 2 || n.Order == nil || n.Bounds == nil || n.Pos.Character <= 1 {
						t.Fatal("VALUES query boundary lost", alias.Input)
					}
				} else {
					n, ok := alias.Input.(*DerivedQuery)
					if !ok || n.Lateral != lateral || n.Query == nil || n.Pos.Character <= 1 {
						t.Fatal("derived query boundary lost", alias.Input)
					}
				}
			}
		})
	}
	for _, sql := range []string{
		"SELECT * FROM LATERAL t",
		"SELECT * FROM LATERAL (t JOIN u ON true)",
		"SELECT * FROM LATERAL (SELECT 1) x unexpected junk",
		"SELECT * FROM LATERAL (VALUES (1)) x(a) extra",
		"SELECT * FROM LATERAL (SELECT 1",
	} {
		statements, err := Parse("SELECT 1; " + sql)
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal("accepted incomplete lateral source", sql, statements, err)
		}
	}
	for _, pair := range [][2]any{
		{DerivedQuery{}, DerivedQuery{Lateral: true}},
		{ValuesQuery{}, ValuesQuery{Lateral: true}},
	} {
		if aggregateUtilitySameSyntax(reflect.ValueOf(pair[0]), reflect.ValueOf(pair[1])) {
			t.Fatal("written source identity lost", pair)
		}
	}
}
