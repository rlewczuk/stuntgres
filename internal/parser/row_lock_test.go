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
			if selectStmt.Locking == nil || selectStmt.Locking.Mode != clause || selectStmt.Locking.Pos.Offset != strings.Index(q, clause) || selectStmt.IsSingleton() {
				t.Fatal(q, selectStmt.Locking)
			}
		}
	}
}
func TestRowLockBoundaries(t *testing.T) {
	for _, suffix := range []string{"FOR UPDATE OF t", "FOR SHARE NOWAIT", "FOR UPDATE SKIP LOCKED", "FOR SHARE FOR UPDATE", "FOR UPDATE junk", "FOR NO SHARE", "FOR wrong SHARE", "FOR NO wrong UPDATE"} {
		statements, err := Parse("SELECT id FROM t " + suffix)
		if err == nil || len(statements) != 0 {
			t.Fatal("excluded clause accepted", suffix, statements, err)
		}
	}
}
