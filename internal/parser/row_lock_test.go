package parser

import (
	"strings"
	"testing"
)

func TestRowLockGrammar(t *testing.T) {
	for _, clause := range []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"} {
		for _, prefix := range []string{`SELECT "T".id FROM public."T" AS "T" WHERE "T".id=$1 ORDER BY id `, "INSERT INTO out SELECT id FROM t WHERE id=$1 ", "INSERT INTO out SELECT FROM t "} {
			q := prefix + "/* lock */ " + clause + ";"
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, statements, err)
			}
			var selectStmt *Select
			switch s := statements[0].(type) {
			case *Select:
				selectStmt = s
			case *Insert:
				selectStmt = s.Source.Select
			default:
				t.Fatal(s)
			}
			if len(selectStmt.Locking) != 1 || selectStmt.Locking[0].Mode != clause || selectStmt.Locking[0].Pos.Offset != strings.Index(q, clause) || selectStmt.From == nil {
				t.Fatal(q, selectStmt.Locking)
			}
		}
	}
}
func TestRowLockBoundaries(t *testing.T) {
	for _, suffix := range []string{"FOR UPDATE junk", "FOR NO SHARE", "FOR wrong SHARE", "FOR NO wrong UPDATE", "FOR UPDATE NOWAIT SKIP LOCKED", "FOR SHARE ORDER BY id", "FOR SHARE LIMIT 1 FOR UPDATE", "FOR UPDATE OF", "FOR SHARE SKIP"} {
		statements, err := Parse("SELECT id FROM t " + suffix)
		if err == nil || len(statements) != 0 {
			t.Fatal("excluded clause accepted", suffix, statements, err)
		}
	}
}

func TestRowLockRepeatedTargetsAndWaits(t *testing.T) {
	for _, tail := range []string{"", " LIMIT 2", " OFFSET 1 FETCH NEXT 2 ROWS ONLY"} {
		for _, before := range []bool{false, true} {
			clause := `FOR UPDATE OF "é",nowait SKIP LOCKED FOR KEY SHARE OF a NOWAIT`
			q := "SELECT a.id FROM t a ORDER BY a.id " + clause + tail
			if before {
				q = "SELECT a.id FROM t a ORDER BY a.id " + tail + " " + clause
			}
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, statements, err)
			}
			s := statements[0].(*Select)
			if len(s.Locking) != 2 || len(s.Locking[0].Targets) != 2 || s.Locking[0].Targets[0].Parts[0].Text != "é" || s.Locking[0].Targets[1].Parts[0].Text != "nowait" || s.Locking[0].Wait != RowLockSkip || s.Locking[1].Wait != RowLockNowait || s.Locking[1].Mode != "FOR KEY SHARE" {
				t.Fatal(q, s.Locking)
			}
			if s.Locking[0].Pos.Offset != strings.Index(q, "FOR UPDATE") || s.Locking[1].Targets[0].Parts[0].Pos.Offset != strings.LastIndex(q, "a NOWAIT") {
				t.Fatal(q, s.Locking)
			}
		}
	}
}

func TestRowLockRejectedShapesRetainStructure(t *testing.T) {
	q := "(SELECT DISTINCT ON(id) id FROM t FOR SHARE OF t) UNION ALL SELECT id FROM u INTERSECT DISTINCT SELECT id FROM t FOR UPDATE NOWAIT"
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	s := statements[0].(*Select)
	if s.Set == nil || s.Set.Operator != "union" || !s.Set.All || s.Set.Left.Distinct == nil || len(s.Set.Left.Locking) != 1 || s.Set.Right.Set == nil || s.Set.Right.Set.Operator != "intersect" || s.Set.Right.Set.All || len(s.Locking) != 1 || s.Locking[0].Wait != RowLockNowait {
		t.Fatal(s)
	}
	if _, err := Parse(q + " junk"); err == nil {
		t.Fatal("malformed trailing text accepted")
	}
}

func TestRowLockDistinctOnStatementFrontier(t *testing.T) {
	for _, q := range []string{
		"SELECT DISTINCT ON(id) id FROM t ORDER BY id FOR UPDATE",
		"SELECT DISTINCT ON(id) id FROM t UNION SELECT id FROM u FOR UPDATE",
		"INSERT INTO dst SELECT DISTINCT ON(id) id FROM t ORDER BY id FOR SHARE",
		"CREATE VIEW marked AS SELECT DISTINCT ON(id) id FROM t ORDER BY id FOR SHARE",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		var s *Select
		switch statement := statements[0].(type) {
		case *Select:
			s = statement
		case *Insert:
			s = statement.Source.Select
		case *CreateView:
			s = statement.Query
		}
		if s == nil || len(s.Locking) != 1 {
			t.Fatal("locking query shape lost", q, s)
		}
		if s.Set != nil {
			s = s.Set.Left
		}
		if s.Distinct == nil || len(s.Distinct.On) != 1 {
			t.Fatal("DISTINCT ON shape lost before locking validation", q, s)
		}
		if _, err := Parse(q + " junk"); err == nil {
			t.Fatal("malformed locking shape accepted", q)
		}
	}
}

func TestRowLockSubqueryClauses(t *testing.T) {
	q := "SELECT (SELECT x FROM scalar_source FOR SHARE OF scalar_source NOWAIT) FROM outer_source,(SELECT x FROM derived_source FOR KEY SHARE OF derived_source SKIP LOCKED) d WHERE EXISTS(SELECT FROM predicate_source FOR UPDATE OF predicate_source) FOR NO KEY UPDATE OF outer_source"
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(q, statements, err)
	}
	outer := statements[0].(*Select)
	scalar := outer.Targets[0].(SubqueryExpr).Query
	derived := outer.From.(*JoinReference).Right.(*AliasReference).Input.(*DerivedQuery).Query
	predicate := outer.Where.(SubqueryExpr).Query
	for _, tc := range []struct {
		query  *Select
		mode   string
		target string
		wait   RowLockWait
	}{
		{outer, "FOR NO KEY UPDATE", "outer_source", RowLockBlock},
		{scalar, "FOR SHARE", "scalar_source", RowLockNowait},
		{derived, "FOR KEY SHARE", "derived_source", RowLockSkip},
		{predicate, "FOR UPDATE", "predicate_source", RowLockBlock},
	} {
		if len(tc.query.Locking) != 1 {
			t.Fatal(tc.query.Locking)
		}
		clause := tc.query.Locking[0]
		if clause.Mode != tc.mode || clause.Wait != tc.wait || len(clause.Targets) != 1 || clause.Targets[0].Parts[0].Text != tc.target || clause.Pos.Offset != strings.Index(q, tc.mode+" OF "+tc.target) {
			t.Fatal(tc, clause)
		}
	}
	wantQueries := []*Select{outer, derived, scalar, predicate}
	wantSources := []string{"outer_source", "derived_source", "scalar_source", "predicate_source"}
	index := 0
	err = VisitQuerySources(outer, nil, func(table *TableReference, query *Select) error {
		if index >= len(wantQueries) || query != wantQueries[index] || table.Name.Parts[0].Text != wantSources[index] {
			t.Fatal("lexical source identity/order lost", index, table, query)
		}
		index++
		return nil
	})
	if err != nil || index != len(wantQueries) {
		t.Fatal(index, err)
	}
}

func TestRowLockSourceManifestQueryEntries(t *testing.T) {
	table := &TableReference{Name: ObjectName{Parts: []Identifier{{Text: "t"}}}}
	leaf := &Select{From: table}
	child := &Select{From: &AliasReference{Input: &DerivedQuery{Query: leaf}, Name: Identifier{Text: "d"}}, Locking: []RowLockClause{{Mode: "FOR UPDATE", Targets: []ObjectName{{Parts: []Identifier{{Text: "d"}}}}}}}
	empty := &Select{}
	outer := &Select{Targets: []Expr{SubqueryExpr{Query: child}, SubqueryExpr{Query: empty}}}
	want := []*Select{outer, child, leaf, empty}
	index, sources := 0, 0
	err := VisitQuerySources(outer, func(query *Select) error {
		if index >= len(want) || query != want[index] {
			t.Fatal("query entry identity/order lost", index, query)
		}
		index++
		return nil
	}, func(source *TableReference, query *Select) error {
		if index != 3 || source != table || query != leaf {
			t.Fatal("source visited outside its lexical entry", index, source, query)
		}
		sources++
		return nil
	})
	if err != nil || index != len(want) || sources != 1 {
		t.Fatal(index, sources, err)
	}
}

func TestRowLockParenthesizedQueryCategories(t *testing.T) {
	q := "SELECT (((SELECT id FROM t FOR UPDATE OF t NOWAIT))) FROM (((SELECT id FROM u FOR SHARE OF u SKIP LOCKED))) d"
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(q, statements, err)
	}
	outer := statements[0].(*Select)
	scalar := outer.Targets[0].(SubqueryExpr)
	derived := outer.From.(*AliasReference).Input.(*DerivedQuery)
	if scalar.Kind != SubqueryScalar || scalar.Pos.Offset != strings.Index(q, "(((") || derived.Pos.Offset != strings.LastIndex(q, "(((") {
		t.Fatal("parenthesis wrapper positions lost", scalar, derived)
	}
	for _, tc := range []struct {
		query  *Select
		source string
		mode   string
		wait   RowLockWait
	}{
		{scalar.Query, "t", "FOR UPDATE", RowLockNowait},
		{derived.Query, "u", "FOR SHARE", RowLockSkip},
	} {
		table, ok := tc.query.From.(*TableReference)
		if !ok || table.Name.Parts[0].Text != tc.source || tc.query.Parenthesized || len(tc.query.Locking) != 1 {
			t.Fatal("query category or local source lost", tc.query)
		}
		clause := tc.query.Locking[0]
		if clause.Mode != tc.mode || clause.Wait != tc.wait || len(clause.Targets) != 1 || clause.Targets[0].Parts[0].Text != tc.source || clause.Pos.Offset != strings.Index(q, tc.mode) {
			t.Fatal("parenthesized rowmark lost", clause)
		}
	}
	if _, err := Parse(q + " junk"); err == nil {
		t.Fatal("trailing input accepted")
	}
}
