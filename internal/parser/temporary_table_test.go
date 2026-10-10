package parser

import (
	"strings"
	"testing"
)

func TestTemporaryTableAST(t *testing.T) {
	for _, c := range []struct {
		q, persistence string
		action         OnCommitAction
	}{
		{"CREATE TABLE t(v INT)", "logged", OnCommitUnspecified},
		{"CREATE TEMP TABLE t(v INT)", "temporary", OnCommitUnspecified},
		{"CREATE TEMPORARY TABLE IF NOT EXISTS pg_temp.t(v INT) ON COMMIT PRESERVE ROWS", "temporary", OnCommitPreserve},
		{"CREATE TABLE pg_temp.t(v INT) ON COMMIT DELETE ROWS", "logged", OnCommitDelete},
		{"CREATE TEMP TABLE t() ON COMMIT DROP", "temporary", OnCommitDrop},
	} {
		stmts, err := Parse(c.q)
		if err != nil || len(stmts) != 1 {
			t.Fatal(c.q, stmts, err)
		}
		table := stmts[0].(*CreateTable)
		if table.Persistence != c.persistence || table.OnCommit != c.action {
			t.Fatal(c.q, table)
		}
		if c.action != OnCommitUnspecified && table.OnCommitPos.Character != strings.Index(c.q, "ON COMMIT")+1 {
			t.Fatal(c.q, table.OnCommitPos)
		}
	}
	for _, q := range []string{
		"CREATE TEMP TABLE t(v INT) ON COMMIT BANANA",
		"CREATE TEMP TABLE t(v INT) ON COMMIT DELETE",
		"CREATE TEMP TABLE t(v INT) ON COMMIT PRESERVE ROW",
		"CREATE TEMP TABLE t(v INT) ON COMMIT DELETE RANGE",
		"CREATE TEMP TABLE t(v INT) ON COMMIT PRESERVE GROUPS",
		"CREATE TEMP TABLE t(v INT) ON COMMIT DROP ON COMMIT DROP",
		"CREATE TEMP TABLE t(v INT) ON COMMIT DROP garbage",
	} {
		if _, err := Parse(q); err == nil {
			t.Fatal("accepted invalid suffix", q)
		}
	}
	stmts, err := Parse("CREATE TABLE preserve(rows INT)")
	if err != nil || stmts[0].(*CreateTable).Elements[0].Column.Name != "rows" {
		t.Fatal(stmts, err)
	}
}
