package stored

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestRuleNamespaceAliases(t *testing.T) {
	qualifier := func(name string) []analyze.ViewNamespaceQualifier {
		return []analyze.ViewNamespaceQualifier{{Name: name}}
	}
	for _, test := range []struct {
		name, outer, sibling, base, want string
		existing                         bool
	}{
		{"recursive_outer_collision", "walk", "walk_1", "walk", "walk_1", true},
		{"recursive_without_collision", "other", "walk", "walk", "walk", true},
		{"generated_outer_collision", "unnamed_subquery", "unnamed_subquery_1", "unnamed_subquery", "unnamed_subquery_1", false},
		{"generated_sibling_is_not_visible", "other", "unnamed_subquery", "unnamed_subquery", "unnamed_subquery", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := analyze.ViewNamespace{Frame: 3, Node: analyze.ViewDerivedNamespace}
			if test.existing {
				current.Qualifiers = qualifier(test.base)
			}
			var item parser.FromItem
			var source plan.Node
			if test.existing {
				table := &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: test.base}}}}
				item, source = table, &plan.WorktableScanNode{DerivedNode: plan.DerivedNode{Table: table}}
			} else {
				child := &parser.Select{}
				item = &parser.DerivedQuery{Query: child}
				source = &plan.DerivedNode{Query: &plan.Query{Root: &plan.ProjectNode{Syntax: child, Frame: 4}}}
			}
			project := &plan.ProjectNode{Frame: 3, Child: source, Syntax: &parser.Select{From: item}}
			view := &View{
				query: &plan.Query{Root: &plan.ProjectNode{Frame: 1, Syntax: &parser.Select{}}},
				namespaces: []analyze.ViewNamespace{
					{Frame: 1, Node: analyze.ViewSourceNamespace, Qualifiers: qualifier(test.outer), Columns: []analyze.ViewNamespaceColumn{{Name: test.want}}},
					{Frame: 2, Node: analyze.ViewSourceNamespace, Qualifiers: qualifier(test.sibling)},
					current,
				},
			}
			fresh := func() *QueryRenderer {
				renderer := NewQueryRenderer(view)
				renderer.Presentation = Presentation{Deparse: true, Rule: true}
				renderer.ruleScopes = []uint32{1, 3}
				// Already allocated ancestor names, not raw current-frame
				// qualifiers, seed the nested range table.
				renderer.ruleTables = map[uint32]ruleRangeTable{1: {used: map[string]int{test.outer: 0}}}
				return renderer
			}
			renderer := fresh()
			for range 2 {
				if err := renderer.prepareRuleRangeTable(context.Background(), catalog.Read{}, project); err != nil {
					t.Fatal(err)
				}
				got, err := renderer.ruleRangeName(current)
				if err != nil || got.name != test.want {
					t.Fatalf("alias = %q, %v; want %q", got.name, err, test.want)
				}
			}
			if !test.existing {
				table := renderer.ruleTables[3]
				got := table.allocate(ruleRenderAlias{3, analyze.ViewDerivedNamespace, 1}, test.base, nil)
				if got.name == test.want {
					t.Fatalf("sibling occurrence reused alias %q", got.name)
				}
			}
			renderer = fresh()
			if err := renderer.prepareRuleRangeTable(context.Background(), catalog.Read{}, project); err != nil {
				t.Fatal(err)
			}
			if got, err := renderer.ruleRangeName(current); err != nil || got.name != test.want {
				t.Fatalf("fresh request alias = %q, %v; want %q", got.name, err, test.want)
			}
		})
	}
}

func TestRuleRecursiveAnchorLabels(t *testing.T) {
	_, _, integer, _ := routineCaptureFixture(t)
	value, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		query, want string
	}{
		{"SELECT 1", "?column?"},
		{"SELECT 1 AS original", "original"},
		{"SELECT * FROM source", "frozen_source"},
	} {
		t.Run(test.query, func(t *testing.T) {
			statements, err := parser.Parse(test.query)
			if err != nil {
				t.Fatal(err)
			}
			syntax := statements[0].(*parser.Select)
			project := &plan.ProjectNode{
				QuerySchema: plan.QuerySchema{{Name: "declared_cte_name", Usage: integer.Usage()}},
				Expressions: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value}},
				Syntax:      syntax, Frame: 2,
			}
			path := analyze.ViewLiteralPath{Query: 2, Clause: analyze.ViewTargetLiteral}
			view := &View{
				query:      &plan.Query{Root: project},
				stars:      map[analyze.ViewLiteralPath][]analyze.ViewColumn{path: {{Frame: 2, Slot: 0}}},
				namespaces: []analyze.ViewNamespace{{Frame: 2, Columns: []analyze.ViewNamespaceColumn{{Name: "frozen_source", Slot: 0}}}},
			}
			for range 2 {
				columns, err := NewQueryRenderer(view).ruleAnchorColumns(syntax, project)
				if err != nil || len(columns) != 1 || columns[0].Name != test.want {
					t.Fatalf("anchor labels = %#v, %v; want %q", columns, err, test.want)
				}
				columns[0] = catalog.Column{Name: "private_edit"}
			}
			if project.Schema()[0].Name != "declared_cte_name" {
				t.Fatal("display mutated executable recursive schema")
			}
		})
	}
}

func TestRuleDerivedSourceAliasReplacement(t *testing.T) {
	read, _, integer, _ := routineCaptureFixture(t)
	value, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	inner := &parser.Select{
		Targets: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}},
		Labels:  []parser.Identifier{{Text: "n"}},
	}
	schema := plan.QuerySchema{{Name: "n", Usage: integer.Usage()}}
	project := &plan.ProjectNode{
		QuerySchema: schema, Child: &plan.SingletonNode{}, Syntax: inner, Frame: 2,
		Expressions: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value}},
	}
	query := &plan.Query{Root: project}
	derived := &plan.DerivedNode{QuerySchema: schema, Query: query, ScanFrame: 1}
	input := &parser.AliasReference{Input: &parser.DerivedQuery{Query: inner}, Name: parser.Identifier{Text: "kept"}}
	siblingSyntax := *inner
	siblingProject := *project
	siblingProject.Syntax, siblingProject.Frame = &siblingSyntax, 3
	siblingQuery := &plan.Query{Root: &siblingProject}
	siblingInput := &parser.DerivedQuery{Query: &siblingSyntax}
	join := &plan.JoinNode{
		Left: derived, Right: &plan.DerivedNode{QuerySchema: schema, Query: siblingQuery, ScanFrame: 1},
		LeftWidth: 1, RightWidth: 1,
	}
	root := &plan.ProjectNode{Child: join, Frame: 1, Syntax: &parser.Select{From: &parser.JoinReference{Left: input, Right: siblingInput}}}
	view := &View{
		query:   &plan.Query{Root: root},
		queries: map[*parser.Select]*plan.ProjectNode{inner: project, &siblingSyntax: &siblingProject},
		literals: map[analyze.ViewLiteralPath]plan.LiteralExpr{
			{Query: 2, Clause: analyze.ViewTargetLiteral}: {Info: expr.Info{Usage: integer.Usage()}, Value: value},
			{Query: 3, Clause: analyze.ViewTargetLiteral}: {Info: expr.Info{Usage: integer.Usage()}, Value: value},
		},
		namespaces: []analyze.ViewNamespace{
			{Frame: 1, Node: analyze.ViewJoinNamespace},
			{Frame: 1, Node: analyze.ViewDerivedNamespace, Ordinal: 2, Qualifiers: []analyze.ViewNamespaceQualifier{{Name: "kept"}}},
			{Frame: 1, Node: analyze.ViewDerivedNamespace, Ordinal: 2},
			{Frame: 1, Node: analyze.ViewDerivedNamespace, Ordinal: 3},
		},
	}
	renderer := NewQueryRenderer(view)
	renderer.Frame, renderer.depth, renderer.functionSources = 1, 1, true
	renderer.Presentation = Presentation{Deparse: true, Rule: true}
	renderer.namespace = 1 // Render the two real input occurrences below.
	from, err := renderer.renderFromSQL(context.Background(), read, input)
	if err != nil || from.text != "( SELECT 1 AS n) kept" || from.slots[0] != "kept.n" {
		t.Fatalf("explicit alias = %q, %#v, %v", from.text, from.slots, err)
	}
	// Replacing the child's default must not consume the unaliased sibling's
	// actual SQL namespace. Declaration and frozen slots use that same name.
	sibling, err := renderer.renderFromSQL(context.Background(), read, siblingInput)
	if err != nil {
		t.Fatal(err)
	}
	if sibling.text != "( SELECT 1 AS n) unnamed_subquery" || sibling.slots[0] != "unnamed_subquery.n" {
		t.Fatalf("unaliased sibling = %q, %#v", sibling.text, sibling.slots)
	}
	if inner.Labels[0].Text != "n" || input.Name.Text != "kept" || project.Schema()[0].Name != "n" {
		t.Fatal("source display mutated retained syntax or schema")
	}
}

func TestRuleCTEBodyPresentationRestoration(t *testing.T) {
	for _, test := range []struct {
		name string
		body parser.Statement
	}{
		{"modifying_body_without_renderer", &parser.Insert{}},
		{"missing_retained_query", &parser.Select{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			renderer := NewQueryRenderer(&View{})
			policy := Presentation{
				Deparse: true, Rule: true, HideLabels: true, AnchorLabels: true,
				Indent: 8, Wrap: 17, Columns: []catalog.Column{{Name: "outer"}},
				Output: sqltype.OutputEnvironment{QuoteAllIdentifiers: true},
			}
			renderer.Presentation = policy
			_, err := renderer.WithClauseSQL(context.Background(), catalog.Read{}, []parser.CommonTableExpression{
				{Name: parser.Identifier{Text: "q"}, Query: test.body},
			}, false)
			if err == nil {
				t.Fatal("invalid retained body unexpectedly rendered")
			}
			if !reflect.DeepEqual(renderer.Presentation, policy) {
				t.Fatalf("CTE error leaked presentation: %#v; want %#v", renderer.Presentation, policy)
			}
		})
	}
}
