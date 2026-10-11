package parser

import (
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestInsertConflictUpdateParser(t *testing.T) {
	for _, head := range []string{"VALUES(1,2)", "(k,v) VALUES(1,2)", "OVERRIDING SYSTEM VALUE VALUES(1,2)", "(k,v) OVERRIDING USER VALUE VALUES(1,2)", "DEFAULT VALUES", "SELECT k,v FROM s", "(SELECT k,v FROM s)"} {
		q := "INSERT INTO t AS dst " + head + " ON /* split */ CONFLICT(k) WHERE k>0 DO UPDATE SET v=excluded.v,k=dst.k WHERE dst.v<excluded.v; SELECT 1; INSERT INTO t VALUES(2,3) ON CONFLICT DO NOTHING"
		statements, err := Parse(q)
		if err != nil || len(statements) != 3 {
			t.Fatal(q, statements, err)
		}
		insert := statements[0].(*Insert)
		if insert.Alias.Text != "dst" || insert.Conflict == nil || !insert.Conflict.Update || len(insert.Conflict.Assignments) != 2 || insert.Conflict.Assignments[0].Target.Text != "v" || insert.Conflict.Assignments[1].Target.Text != "k" || insert.Conflict.Predicate == nil || insert.Conflict.Where == nil || insert.Conflict.OnPos.Offset != strings.Index(q, "ON /* split */") {
			t.Fatal("action AST ordering/scopes", insert)
		}
		if _, ok := statements[1].(*Select); !ok {
			t.Fatal("INSERT lexer context leaked", statements[1])
		}
		following := statements[2].(*Insert)
		if following.Conflict == nil || following.Conflict.Update || len(following.Conflict.Assignments) != 0 {
			t.Fatal("action discriminator leaked", following)
		}
	}
	// Admission must enumerate the very same source occurrences as binding:
	// producer, all SET RHS, all destination bounds, action WHERE, then output.
	queries, err := Parse("INSERT INTO t SELECT k,a FROM input_rows ON CONFLICT(k) DO UPDATE SET a[(SELECT k FROM limits)]=(SELECT v FROM rhs),k=(SELECT k FROM rhs2) WHERE (SELECT enabled FROM conditions) RETURNING (SELECT v FROM output_rows)")
	if err != nil || len(queries) != 1 {
		t.Fatal(queries, err)
	}
	var sources []string
	if err := VisitQuerySources(queries[0], nil, func(table *TableReference, _ *Select) error {
		sources = append(sources, table.Name.Parts[len(table.Name.Parts)-1].Text)
		return nil
	}); err != nil || strings.Join(sources, ",") != "input_rows,rhs,rhs2,limits,conditions,output_rows" {
		t.Fatal("complete source/action admission in binding order", sources, err)
	}
	for _, tail := range []string{"", " ON CONFLICT DO NOTHING"} {
		statements, err := Parse("INSERT INTO t AS dst DEFAULT VALUES" + tail)
		if err != nil || len(statements) != 1 || statements[0].(*Insert).Alias.Text != "dst" {
			t.Fatal("ordinary alias", tail, err)
		}
	}
	for _, q := range []string{"INSERT INTO t VALUES(1) ON CONFLICT(k) DO UPDATE SET k=2 FROM t", "INSERT INTO t VALUES(1) ON CONFLICT(k) DO UPDATE SET k=2 WHERE TRUE trailing"} {
		statements, err := Parse(q)
		var d *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &d) || d.Code != "42601" {
			t.Fatal("bounded action grammar", q, statements, err)
		}
	}
	statements, err := Parse("INSERT INTO t VALUES(1) ON CONFLICT(k) DO UPDATE SET k=2 RETURNING k")
	if err != nil || len(statements) != 1 || statements[0].(*Insert).Returning == nil || len(statements[0].(*Insert).Returning.Targets) != 1 {
		t.Fatal("RETURNING AST", statements, err)
	}
}
