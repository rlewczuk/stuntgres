package parser

import (
	"reflect"
	"strings"
	"testing"
)

func TestSystemRowsParser(t *testing.T) {
	for _, source := range []string{"sr", "ONLY sr", "ONLY (sr)", "sr AS sampled", "sr sampled(id)"} {
		sql := "SELECT * FROM " + source + " TABLESAMPLE sampling.system_rows($1) REPEATABLE($2)"
		statements, err := Parse(sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(source, statements, err)
		}
		query := statements[0].(*Select)
		tables := FromTables(query.From)
		if len(tables) != 1 || tables[0].Sample == nil {
			t.Fatal("sampling source lost", source, query.From)
		}
		sample := tables[0].Sample
		if len(sample.Method.Parts) != 2 || sample.Method.Parts[0].Text != "sampling" || sample.Method.Parts[1].Text != "system_rows" || sample.Method.Parts[0].Pos.Character != strings.Index(sql, "sampling.system_rows")+1 {
			t.Fatal(sample)
		}
		if len(sample.Arguments) != 1 || sample.Arguments[0].(ParameterExpr).Number != 1 || sample.Repeatable.(ParameterExpr).Number != 2 {
			t.Fatal(sample)
		}
		var parameters []int
		if err := VisitQueryExpressions(query, func(raw Expr) error {
			if p, ok := raw.(ParameterExpr); ok {
				parameters = append(parameters, p.Number)
			}
			return nil
		}); err != nil || !reflect.DeepEqual(parameters, []int{1, 2}) {
			t.Fatal(parameters, err)
		}
		if strings.Contains(source, "sampled") {
			alias, ok := query.From.(*AliasReference)
			if !ok || alias.Name.Text != "sampled" {
				t.Fatal("alias boundary lost", query.From)
			}
		}
	}
	for _, sql := range []string{
		"SELECT * FROM sr TABLESAMPLE system_rows()",
		"SELECT * FROM sr TABLESAMPLE system_rows(1) AS invalid_alias",
		"SELECT * FROM sr TABLESAMPLE system_rows(1) trailing_junk",
		"SELECT * FROM sr TABLESAMPLE system_rows(1) TABLESAMPLE system_rows(2)",
	} {
		if _, err := Parse(sql); err == nil {
			t.Fatal("accepted incomplete or misplaced source", sql)
		}
	}
	statements, err := Parse("SELECT * FROM sr TABLESAMPLE system_rows((SELECT n FROM counts))")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	if err := VisitQuerySources(statements[0], nil, func(table *TableReference, _ *Select) error {
		tables = append(tables, table.Name.Parts[0].Text)
		return nil
	}); err != nil || !reflect.DeepEqual(tables, []string{"sr", "counts"}) {
		t.Fatal("sampling subquery omitted or reordered during admission", tables, err)
	}
}
