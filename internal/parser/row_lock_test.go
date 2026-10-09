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
			if len(selectStmt.Locking) != 1 || selectStmt.Locking[0].Mode != clause || selectStmt.Locking[0].Pos.Offset != strings.Index(q, clause) || selectStmt.IsSingleton() {
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
