package parser

import (
	"testing"
	"stuntgres/internal/sqlstate"
)

func TestQueryLimitGrammar(t *testing.T) {
	for _,query := range []string{
		"SELECT id FROM t LIMIT $1 OFFSET $2",
		"SELECT id FROM t OFFSET NULL LIMIT ALL",
		"SELECT id FROM t OFFSET 1 ROW FETCH NEXT (1+1) ROWS ONLY",
		"SELECT id FROM t FETCH FIRST ROW ONLY OFFSET 1 ROWS",
		"SELECT id FROM t LIMIT nextval('s')",
		"SELECT id FROM t LIMIT random()",
		"SELECT id FROM t ORDER BY id FOR UPDATE LIMIT 1",
		"SELECT id FROM t ORDER BY id LIMIT 1 FOR UPDATE",
		"SELECT nextval('s') LIMIT 0",
		"INSERT INTO t VALUES(1),(2) LIMIT 1",
		"INSERT INTO t (VALUES(1),(2) OFFSET 1 FETCH NEXT ROW ONLY)",
		"INSERT INTO t (SELECT LIMIT 0)",
		"INSERT INTO t SELECT 1 LIMIT 0",
	} {
		t.Run(query,func(t *testing.T) {
			statements,err := Parse(query)
			if err != nil || len(statements)!=1 { t.Fatal(statements,err) }
			switch s := statements[0].(type) {
			case *Select:
				if s.Bounds == nil { t.Fatal("lost count decoration") }
			case *Insert:
				if s.Source == nil { t.Fatal("decorated producer remained contextual VALUES") }
			default: t.Fatalf("unexpected statement %T",s)
			}
		})
	}
	for _,query := range []string{
		"SELECT 1 LIMIT 1", "VALUES(1) LIMIT 1",
		"SELECT id FROM t LIMIT 1 LIMIT 2", "SELECT id FROM t OFFSET 1 OFFSET 2",
		"SELECT id FROM t LIMIT 1 FETCH FIRST ROW ONLY",
		"SELECT id FROM t FETCH FIRST 1+1 ROW ONLY",
		"SELECT id FROM t FETCH FIRST 1 ROW WITH TIES",
		"SELECT id FROM t LIMIT 1 junk", "SELECT id FROM t LIMIT",
	} {
		t.Run("reject/"+query,func(t *testing.T) {
			statements,err := Parse("INSERT INTO t VALUES(9);"+query)
			if err == nil || statements != nil { t.Fatal("partial script accepted",statements,err) }
		})
	}
	_,err := Parse("SELECT id FROM t LIMIT 1,2")
	d,ok := err.(*sqlstate.Error)
	if !ok || d.Code!="42601" || d.Message!="LIMIT #,# syntax is not supported" || d.Hint!="Use separate LIMIT and OFFSET clauses." || d.Position!="18" { t.Fatal(err) }
}
