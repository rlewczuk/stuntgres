package parser

import "testing"

func TestStringSplitSyntax(t *testing.T) {
	for _, query := range []string{
		"SELECT rows FROM t",
		"SELECT a,rows FROM (VALUES(7,'kept')) s(a,rows)",
		"SELECT count(*) OVER(ROWS 1 PRECEDING) FROM t",
		"SELECT split_part('a,b',',',-1),string_to_array('a,b',',',NULL)",
		"SELECT * FROM string_to_table('a,b',',') WITH ORDINALITY AS t(v,n)",
		"SELECT * FROM LATERAL string_to_table('a,b',',') t(v)",
		"SELECT * FROM ROWS FROM(string_to_table('a,b',','),pg_catalog.string_to_table('x',',')) WITH ORDINALITY t(a,b,n)",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
	statements, err := Parse("SELECT * FROM ROWS FROM(string_to_table((SELECT v FROM source_input),','),string_to_table('x',',')) WITH ORDINALITY t(a,b,n)")
	if err != nil {
		t.Fatal(err)
	}
	source := statements[0].(*Select).From.(*AliasReference).Input.(*FunctionTable)
	if !source.RowsFrom || !source.Ordinality || source.Lateral || len(source.Calls) != 2 {
		t.Fatal(source)
	}
	visited := 0
	if err := VisitQuerySources(statements[0].(*Select), nil, func(table *TableReference, _ *Select) error {
		visited++
		if table.Name.Parts[0].Text != "source_input" {
			t.Fatal(table.Name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if visited != 1 {
		t.Fatal("function argument query source walk", visited)
	}
	if _, err := Parse("SELECT * FROM \"rows\" FROM(string_to_table('a',','))"); err == nil {
		t.Fatal("quoted ROWS became a keyword")
	}
}
