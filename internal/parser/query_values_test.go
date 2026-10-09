package parser

import "testing"

func TestQueryFromValuesAST(t *testing.T) {
	for _, q := range []string{"VALUES(1),(NULL) ORDER BY column1 LIMIT 1", "((VALUES(1),(NULL) ORDER BY column1 LIMIT 1))"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		s := statements[0].(*Select)
		if s.Values == nil || s.From != nil || len(s.Values.Rows) != 2 || s.Values.Order == nil || s.Values.Bounds == nil || s.Values.Pos.Character < 1 {
			t.Fatal(q, s)
		}
	}
	statements, err := Parse("SELECT v.* FROM (VALUES($1,DEFAULT),(2,3) ORDER BY column1 LIMIT 0) AS v(n)")
	if err != nil {
		t.Fatal(err)
	}
	s := statements[0].(*Select)
	alias := s.From.(*AliasReference)
	values := alias.Input.(*ValuesQuery)
	if alias.Name.Text != "v" || len(alias.Columns) != 1 || alias.Columns[0].Text != "n" || len(FromTables(s.From)) != 0 {
		t.Fatal(alias)
	}
	if values.Rows[0][0].(ParameterExpr).Number != 1 || values.Rows[0][1].(DefaultExpr).Pos.Character != 28 {
		t.Fatal(values.Rows)
	}
	for _, q := range []string{"INSERT INTO t VALUES('1')", "INSERT INTO t (VALUES('1'))", "INSERT INTO t VALUES('1') ORDER BY 1"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		insert := statements[0].(*Insert)
		decorated := q == "INSERT INTO t VALUES('1') ORDER BY 1"
		if (insert.Source != nil) != decorated || (len(insert.Rows) != 0) == decorated {
			t.Fatal(q, insert)
		}
		if decorated && (insert.Source.Values == nil || insert.Source.Select != nil) {
			t.Fatal(insert.Source)
		}
	}
}

func TestQueryFromValuesCellExpressions(t *testing.T) {
	statements, err := Parse("VALUES(missing,t.n,count(*),1+2,TRUE AND FALSE)")
	if err != nil {
		t.Fatal(err)
	}
	row := statements[0].(*Select).Values.Rows[0]
	if len(row) != 5 {
		t.Fatal(row)
	}
	if _, ok := row[0].(ColumnExpr); !ok {
		t.Fatal(row[0])
	}
	if _, ok := row[1].(ColumnExpr); !ok {
		t.Fatal(row[1])
	}
	if _, ok := row[2].(AggregateExpr); !ok {
		t.Fatal(row[2])
	}
	if _, ok := row[3].(OperatorExpr); !ok {
		t.Fatal(row[3])
	}
	if _, ok := row[4].(LogicalExpr); !ok {
		t.Fatal(row[4])
	}
}

func TestQueryFromValuesGroupingBoundary(t *testing.T) {
	keywordDiagnostic(t, "SELECT v.n FROM ((VALUES(1)) v(n)) j", "42601", `syntax error at or near ")"`, "34")
	for _, query := range []string{
		"SELECT v.n FROM ((VALUES(1))) v(n)",
		"SELECT j.n FROM (t JOIN (VALUES(1)) v(n) USING(n)) j",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
	}
}
