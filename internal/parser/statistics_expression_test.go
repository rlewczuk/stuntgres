package parser

import "testing"

func TestStatisticsExpressionElementsGrammar(t *testing.T) {
	for _, sql := range []string{
		"CREATE STATISTICS s ON (a+1) FROM t",
		"CREATE STATISTICS s (ndistinct) ON (a+1),b FROM t",
		"CREATE STATISTICS ON (a+1),(b+2) FROM t",
		"CREATE STATISTICS IF NOT EXISTS s ON random(),b FROM t",
		"CREATE STATISTICS s ON (coalesce(a,0)) FROM t",
		"CREATE STATISTICS s ON (t.a) FROM t",
	} {
		statements, err := Parse(sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(sql, statements, err)
		}
		s, ok := statements[0].(*CreateStatistics)
		if !ok || len(s.Elements) == 0 {
			t.Fatal("missing ordered elements", sql, s)
		}
	}
	statements, err := Parse("CREATE STATISTICS s ON (t.a),b,(a+1) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	s := statements[0].(*CreateStatistics)
	first, ok := s.Elements[0].(ColumnExpr)
	if !ok || len(first.Name.Parts) != 2 || first.Name.Parts[0].Text != "t" || first.Name.Parts[1].Text != "a" {
		t.Fatal("grouping changed column syntax", s.Elements)
	}
	if second, ok := s.Elements[1].(ColumnExpr); !ok || second.Name.Parts[0].Text != "b" {
		t.Fatal("source order lost", s.Elements)
	}
	for _, sql := range []string{"CREATE STATISTICS s ON FROM t", "CREATE STATISTICS s ON (a+1), FROM t", "CREATE STATISTICS s ON (a+1) FROM t trailing"} {
		if statements, err := Parse(sql); err == nil || len(statements) != 0 {
			t.Fatal("accepted incomplete expression definition", sql, statements, err)
		}
	}
}
