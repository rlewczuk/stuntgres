package parser

import "testing"

func TestCreateViewGrammar(t *testing.T) {
	for _, query := range []string{
		"CREATE VIEW v AS SELECT id FROM t",
		"CREATE VIEW public.v(k,label) AS SELECT id,txt FROM t WHERE visible",
		"CREATE VIEW v(k) WITH (security_barrier=true,security_invoker=false) AS SELECT id,txt FROM t",
		"CREATE VIEW v AS SELECT count(*) FROM t GROUP BY id HAVING count(*)>1",
		"CREATE VIEW z AS SELECT FROM t",
	} {
		t.Run(query, func(t *testing.T) {
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
		})
	}
}

func TestCreateViewConsumesEntireInput(t *testing.T) {
	for _, query := range []string{
		"CREATE VIEW v AS SELECT id FROM t garbage extra_token",
		"CREATE VIEW v AS SELECT id FROM t; SELECT FROM",
	} {
		statements, err := Parse(query)
		if err == nil || len(statements) != 0 {
			t.Fatal(query, statements, err)
		}
	}
}

func TestCreateRecursiveViewLowering(t *testing.T) {
	statements, err := Parse("WITH local(n) AS (SELECT 1) SELECT n,0 AS depth FROM local UNION SELECT n,depth+1 FROM r WHERE depth<2")
	if err != nil {
		t.Fatal(err)
	}
	body := statements[0].(*Select)
	set, with := body.Set, body.With[0].Query
	name := ObjectName{Parts: []Identifier{{Text: "public"}, {Text: "r"}}}
	columns := []Identifier{{Text: "n"}}
	query := recursiveViewSelect(name, columns, body)
	if !query.WithRecursive || len(query.With) != 1 || query.With[0].Query != body {
		t.Fatalf("recursive body identity lost: %#v", query)
	}
	if query.With[0].Name.Text != "r" || len(query.With[0].Columns) != 1 || query.With[0].Columns[0].Text != "n" {
		t.Fatalf("wrong lexical namespace: %#v", query.With[0])
	}
	if len(query.Targets) != 1 {
		t.Fatalf("short alias list must project one output: %#v", query.Targets)
	}
	target, ok := query.Targets[0].(ColumnExpr)
	if !ok || len(target.Name.Parts) != 1 || target.Name.Parts[0].Text != "n" {
		t.Fatalf("expected explicit unqualified projection: %#v", query.Targets[0])
	}
	from, ok := query.From.(*TableReference)
	if !ok || len(from.Name.Parts) != 1 || from.Name.Parts[0].Text != "r" {
		t.Fatalf("expected unqualified self source: %#v", query.From)
	}
	if body.Set != set || body.With[0].Query != with || len(LeftmostSelect(body).Targets) != 2 {
		t.Fatal("lowering changed the original WITH/set body or hidden tuple width")
	}
	query.With[0].Columns[0].Text = "changed"
	if columns[0].Text != "n" {
		t.Fatal("synthetic aliases share destination storage")
	}
}

func TestCreateRecursiveViewSyntheticPositions(t *testing.T) {
	for _, sql := range []string{
		`CREATE RECURSIVE VIEW public.r(n) AS SELECT 7`,
		`/*é*/ CREATE RECURSIVE VIEW public."Récur"("Né") AS SELECT 7`,
	} {
		t.Run(sql, func(t *testing.T) {
			statements, err := Parse(sql)
			if err != nil {
				t.Fatal(err)
			}
			view := statements[0].(*CreateView)
			query := view.Query
			body := query.With[0].Query.(*Select)
			if view.Pos.Character == 0 || view.Name.Parts[0].Pos.Character == 0 || view.Name.Parts[1].Pos.Character == 0 || view.Columns[0].Pos.Character == 0 || body.Pos.Character == 0 || body.Targets[0].SourcePosition().Character == 0 {
				t.Fatal("original CREATE/destination/list/body positions lost")
			}
			positions := []Position{query.Pos, query.With[0].Name.Pos, query.With[0].Columns[0].Pos}
			for _, target := range query.Targets {
				column := target.(ColumnExpr)
				positions = append(positions, column.Pos)
				for _, part := range column.Name.Parts {
					positions = append(positions, part.Pos)
				}
			}
			for _, part := range query.From.(*TableReference).Name.Parts {
				positions = append(positions, part.Pos)
			}
			for _, pos := range positions {
				if pos != (Position{}) {
					t.Fatalf("generated position = %#v", pos)
				}
			}
			before := view.Columns[0]
			query.With[0].Columns[0].Pos = Position{Offset: 999, Character: 999}
			if view.Columns[0] != before {
				t.Fatal("lowering aliases mutated destination positions")
			}
		})
	}
}
