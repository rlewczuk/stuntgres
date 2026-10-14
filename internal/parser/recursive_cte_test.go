package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestCTEParserRecursiveModifierAndCompleteInput(t *testing.T) {
	for _, tail := range []string{
		"SELECT n FROM r",
		"INSERT INTO target SELECT n FROM r",
		"UPDATE target SET n=1 WHERE n IN (SELECT n FROM r)",
		"DELETE FROM target WHERE n IN (SELECT n FROM r)",
	} {
		statements, err := Parse("/*é*/ WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<3) " + tail)
		if err != nil || len(statements) != 1 || !StatementWithRecursive(statements[0]) || len(StatementWith(statements[0])) != 1 {
			t.Fatal(statements, err)
		}
		if StatementWith(statements[0])[0].Name.Pos.Character != 22 {
			t.Fatal("Unicode CTE position", StatementWith(statements[0])[0].Name.Pos)
		}
	}
	for _, text := range []string{"SELECT recursive FROM recursive", "CREATE TABLE recursive(recursive INT)", "WITH RECURSIVE recursive(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM recursive WHERE n<3) SELECT n FROM recursive"} {
		statements, err := Parse(text)
		if err != nil || len(statements) != 1 {
			t.Fatal("contextual recursive identifier", text, err)
		}
	}
	statements, err := Parse("WITH RECURSIVE r AS (SELECT 1) SELECT * FROM r; @")
	if len(statements) != 0 || err == nil {
		t.Fatal("accepted incomplete script", statements, err)
	}
	statements, err = Parse("WITH RECURSIVE r AS (SELECT 1) MERGE INTO target USING r ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(1)")
	var diagnostic *sqlstate.Error
	if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "WITH RECURSIVE is not supported for MERGE statement" || diagnostic.Position != "" {
		t.Fatal(statements, err)
	}
}

func TestCTEParserRecursiveTopologyAndStableClassification(t *testing.T) {
	statements, err := Parse("WITH RECURSIVE a AS (SELECT n FROM b), b(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM b WHERE n<3) SELECT a.n,p.n FROM a CROSS JOIN public.b p")
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := AnalyzeRecursiveCTEs(statements[0])
	if err != nil || !reflect.DeepEqual(analysis.Order, []int{1, 0}) || !reflect.DeepEqual(analysis.Recursive, []bool{false, true}) {
		t.Fatal(analysis, err)
	}
	for range 2 {
		var physical, virtual []string
		err := VisitQuerySources(statements[0], nil, func(table *TableReference, _ *Select) error {
			physical = append(physical, table.Name.Parts[len(table.Name.Parts)-1].Text)
			return nil
		}, func(table *TableReference, isVirtual bool, pending string) {
			if pending != "" {
				t.Fatal("recursive source remained pending", pending)
			}
			if isVirtual {
				virtual = append(virtual, table.Name.Parts[0].Text)
			}
		})
		if err != nil || !reflect.DeepEqual(physical, []string{"b"}) || !reflect.DeepEqual(virtual, []string{"b", "b", "a"}) {
			t.Fatal(physical, virtual, err)
		}
	}
}

func TestCTEParserRecursiveLexicalShadowIdentity(t *testing.T) {
	for _, tc := range []struct {
		sql       string
		recursive bool
	}{
		{"WITH RECURSIVE r AS (SELECT n FROM (WITH r AS (SELECT 1 AS n) SELECT n FROM r) d) SELECT * FROM r", false},
		{"WITH RECURSIVE r AS (SELECT n FROM (WITH RECURSIVE a AS (SELECT n FROM r),r AS (SELECT 1 AS n) SELECT n FROM a) d) SELECT * FROM r", false},
		{"WITH RECURSIVE r AS (SELECT n FROM public.r) SELECT * FROM r", false},
	} {
		statements, err := Parse(tc.sql)
		if err != nil {
			t.Fatal(err)
		}
		analysis, err := AnalyzeRecursiveCTEs(statements[0])
		if err != nil || analysis.Recursive[0] != tc.recursive {
			t.Fatal(analysis, err)
		}
	}
}

func TestCTEParserRecursiveContextPrecedence(t *testing.T) {
	for _, tc := range []struct{ sql, message string }{
		{"WITH RECURSIVE r(n) AS (SELECT n FROM r UNION ALL SELECT 1) SELECT * FROM r", `recursive reference to query "r" must not appear within its non-recursive term`},
		{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT a.n FROM r a WHERE EXISTS(SELECT n FROM r)) SELECT * FROM r", `recursive reference to query "r" must not appear within a subquery`},
		{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT a.n FROM r a CROSS JOIN r b) SELECT * FROM r", `recursive reference to query "r" must not appear more than once`},
	} {
		statements, err := Parse(tc.sql)
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42P19" || diagnostic.Message != tc.message {
			t.Fatal(statements, err)
		}
	}
	visited := 0
	query := &Select{WithRecursive: true, With: []CommonTableExpression{{Name: Identifier{Text: "r", Pos: Position{Character: 16}}, Query: &Select{}}, {Name: Identifier{Text: "r", Pos: Position{Character: 35}}, Query: &Select{From: &TableReference{Name: ObjectName{Parts: []Identifier{{Text: "missing"}}}}}}}}
	err := VisitQuerySources(query, func(*Select) error { visited++; return nil }, func(*TableReference, *Select) error { visited++; return nil })
	var diagnostic *sqlstate.Error
	if visited != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42712" {
		t.Fatal("duplicate lost admission precedence", visited, err)
	}
}

func TestCTEParserRecursiveBodyWithPrecedence(t *testing.T) {
	const subquery = `recursive reference to query "r" must not appear within a subquery`
	for _, tc := range []struct{ name, body, message string }{
		{"before_order", "WITH a AS (SELECT n FROM r AS forbidden) SELECT 1 UNION ALL SELECT n FROM a ORDER BY n", subquery},
		{"source_order", "WITH a AS (SELECT n FROM r AS forbidden),b AS (SELECT n FROM r AS later) SELECT 1 UNION ALL SELECT n FROM a LIMIT 2", subquery},
		{"form_first", "WITH a AS (SELECT n FROM r AS forbidden) SELECT n FROM a ORDER BY n", `recursive query "r" does not have the form non-recursive-term UNION [ALL] recursive-term`},
		{"nested_shadow", "WITH a AS (WITH r AS (SELECT 7 AS n) SELECT n FROM r) SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<3", ""},
		{"qualified_physical", "WITH a AS (SELECT n FROM public.r) SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<3", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Parse the isolated body, then supply the outer definition identity.
			// This exercises analysis directly without Parse's prewalk swallowing
			// the invalid statement before its lexical graph can be inspected.
			statements, err := Parse(tc.body)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			statement := &Select{WithRecursive: true, With: []CommonTableExpression{{Name: Identifier{Text: "r", Pos: Position{Character: 16}}, Query: statements[0]}}}
			analysis, err := AnalyzeRecursiveCTEs(statement)
			if !reflect.DeepEqual(analysis.Recursive, []bool{true}) {
				t.Fatal(analysis, err)
			}
			if tc.message == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			position := strconv.Itoa(strings.Index(tc.body, "r AS forbidden") + 1)
			if tc.name == "form_first" {
				position = "16"
			}
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P19", Message: tc.message, Position: position}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				t.Fatalf("got %+v want %+v", err, want)
			}
			visited := 0
			err = VisitQuerySources(statement, func(*Select) error { visited++; return nil }, func(*TableReference, *Select) error { visited++; return nil })
			if visited != 0 || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				t.Fatal("legality lost pre-admission precedence", visited, err)
			}
		})
	}
}

func TestCTEParserRecursiveLegalitySubLinkTraversal(t *testing.T) {
	statements, err := Parse("SELECT (SELECT n FROM test_ref) IN (SELECT n FROM query_ref)")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	visible := []*CommonTableExpression{{Name: Identifier{Text: "test_ref"}}, {Name: Identifier{Text: "query_ref"}}}
	for _, context := range []string{"", "non-recursive term", "outer join", "intersect", "except"} {
		t.Run(context, func(t *testing.T) {
			var got []string
			err := visitQuerySources(statements[0], nil, nil, nil, func(table *TableReference, target *CommonTableExpression, context string) error {
				if target != visible[0] && target != visible[1] {
					t.Fatal("lost definition identity", target)
				}
				got = append(got, table.Name.Parts[0].Text+":"+context)
				return nil
			}, visible, context, true)
			if err != nil || !reflect.DeepEqual(got, []string{"query_ref:subquery", "test_ref:subquery"}) {
				t.Fatal(got, err)
			}
		})
	}
	// The same tree's physical-source admission and dependency discovery retain
	// Left-first order. Legality must not reorder their captured occurrences.
	var physical, dependencies []string
	err = VisitQuerySources(statements[0], nil, func(table *TableReference, _ *Select) error {
		physical = append(physical, table.Name.Parts[0].Text)
		return nil
	})
	if err != nil || !reflect.DeepEqual(physical, []string{"test_ref", "query_ref"}) {
		t.Fatal(physical, err)
	}
	err = visitQuerySources(statements[0], nil, nil, nil, func(table *TableReference, _ *CommonTableExpression, _ string) error {
		dependencies = append(dependencies, table.Name.Parts[0].Text)
		return nil
	}, visible, "", false)
	if err != nil || !reflect.DeepEqual(dependencies, physical) {
		t.Fatal(dependencies, err)
	}
	primary := errors.New("subselect failure")
	var visited []string
	err = visitQuerySources(statements[0], nil, nil, nil, func(table *TableReference, _ *CommonTableExpression, _ string) error {
		visited = append(visited, table.Name.Parts[0].Text)
		return primary
	}, visible, "non-recursive term", true)
	if err != primary || !reflect.DeepEqual(visited, []string{"query_ref"}) {
		t.Fatal("testexpr ran after primary subselect failure", visited, err)
	}
}

func TestCTEParserRecursiveLegalityContextRestoration(t *testing.T) {
	// Consecutive WITH bodies share the walk's enclosing context. The second
	// definition's direct reference must not inherit the first body's SubLink.
	statements, err := Parse("WITH a AS (SELECT (SELECT n FROM query_ref)),b AS (SELECT n FROM after_ref) SELECT * FROM b")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	visible := []*CommonTableExpression{{Name: Identifier{Text: "query_ref"}}, {Name: Identifier{Text: "after_ref"}}}
	for _, context := range []string{"", "non-recursive term", "outer join", "intersect", "except"} {
		t.Run(context, func(t *testing.T) {
			for range 2 {
				var got []string
				err := visitQuerySources(statements[0], nil, nil, nil, func(table *TableReference, target *CommonTableExpression, context string) error {
					if target == visible[0] || target == visible[1] {
						got = append(got, table.Name.Parts[0].Text+":"+context)
					}
					return nil
				}, visible, context, true)
				if err != nil || !reflect.DeepEqual(got, []string{"query_ref:subquery", "after_ref:" + context}) {
					t.Fatal("SubLink leaked context or repeat mutated identity", got, err)
				}
			}
		})
	}
}
