package parser

import "testing"

func TestJSONTableParser(t *testing.T) {
	statements, err := Parse(`/*é🙂*/ SELECT * FROM LATERAL JSON_TABLE($1::jsonb,'$' AS root PASSING $2 AS x COLUMNS(r FOR ORDINALITY,NESTED PATH '$.a[*]' AS child COLUMNS(v text FORMAT JSON PATH '$' WITHOUT WRAPPER KEEP QUOTES DEFAULT $3 ON EMPTY),p int DEFAULT $4 ON ERROR)) AS j`)
	if err != nil {
		t.Fatal(err)
	}
	table := statements[0].(*Select).From.(*AliasReference).Input.(*JSONTable)
	if !table.Lateral || table.PathName.Text != "root" || len(table.Columns) != 3 || table.Columns[1].PathName.Text != "child" || table.Pos.Character != 30 {
		t.Fatalf("raw source: %#v", table)
	}
	column := table.Columns[1].Columns[0]
	if column.Format == nil || column.Wrapper != "without" || column.Quotes != "keep" || column.Empty.Default == nil {
		t.Fatalf("column options: %#v", column)
	}
	children := table.Expressions()
	if len(children) != 4 {
		t.Fatalf("written children: %d", len(children))
	}
	next := table.WithExpressions(children)
	next.Passing[0].Name.Text = "changed"
	next.Columns[1].Columns[0].Name.Text = "changed"
	if table.Passing[0].Name.Text != "x" || table.Columns[1].Columns[0].Name.Text != "v" {
		t.Fatal("replacement aliases original storage")
	}
	var parameters []int
	if err := VisitQueryExpressions(statements[0], func(child Expr) error {
		if p, ok := child.(ParameterExpr); ok {
			parameters = append(parameters, p.Number)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(parameters) != 4 {
		t.Fatalf("visitor lost expression: %v", parameters)
	}
	// Generic and keyword type spellings must finish before the EXISTS clause,
	// rather than entering the table-column two-word invalid-type diagnostic.
	for _, typ := range []string{"boolean", "bool", "int", "integer", "text", "smallint", "bigint", "real", "jt_domain", "public.jt_domain", "varchar(3)", "int[]"} {
		statements, err := Parse(`SELECT * FROM JSON_TABLE('{}','$' COLUMNS(e ` + typ + ` EXISTS PATH '$.x' FALSE ON ERROR))`)
		if err != nil {
			t.Fatalf("EXISTS after %s: %v", typ, err)
		}
		column := statements[0].(*Select).From.(*JSONTable).Columns[0]
		if !column.Exists || column.Path == nil || column.Error.Kind != "false" {
			t.Fatalf("EXISTS clause after %s: %#v", typ, column)
		}
	}
	for _, query := range []string{
		`SELECT * FROM JSON_TABLE('{}','$' COLUMNS())`,
		`SELECT * FROM JSON_TABLE('{}','$' COLUMNS(v bool EXISTS NULL ON EMPTY))`,
		`SELECT * FROM JSON_TABLE('{}','$' COLUMNS(v int)); garbage`,
	} {
		if _, err := Parse(query); err == nil {
			t.Fatalf("invalid source accepted: %s", query)
		}
	}
	if _, err := Parse(`SELECT columns,nested,path FROM t`); err != nil {
		t.Fatalf("clause tokens leaked: %v", err)
	}
}
