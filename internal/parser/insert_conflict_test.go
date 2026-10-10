package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestInsertConflictAcceptedContextReset(t *testing.T) {
	script := "INSERT INTO t SELECT k FROM s WHERE active ON CONFLICT DO NOTHING; SELECT k FROM s WHERE active; INSERT INTO t VALUES(2) ON /* comment */ CONFLICT(k) DO NOTHING"
	statements, err := Parse(script)
	if err != nil || len(statements) != 3 {
		t.Fatal(statements, err)
	}
	first, ok := statements[0].(*Insert)
	if !ok || first.Conflict == nil {
		t.Fatal("first INSERT lost conflict", statements[0])
	}
	second, ok := statements[1].(*Select)
	if !ok || second.Where == nil {
		t.Fatal("standalone SELECT lost predicate", statements[1])
	}
	third, ok := statements[2].(*Insert)
	if !ok || third.Conflict == nil || len(third.Conflict.Keys) != 1 {
		t.Fatal("next INSERT context was not entered", statements[2])
	}
	query := "INSERT INTO t VALUES(9) ON CONFLICT DO NOTHING; SELECT k FROM s WHERE active ON CONFLICT"
	statements, err = Parse(query)
	var diagnostic *sqlstate.Error
	position := utf8.RuneCountInString(query[:strings.LastIndex(query, "ON CONFLICT")]) + 1
	if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "predicate syntax is unsupported" || diagnostic.Position != strconv.Itoa(position) {
		t.Fatal("accepted context leaked into standalone guard", statements, err)
	}
}

func TestInsertConflictDirectAggregateKeyClassification(t *testing.T) {
	for _, key := range []string{"sum(k)", "pg_catalog.sum(k)", "row_number(k)"} {
		statements, err := Parse("INSERT INTO t VALUES(1) ON CONFLICT(" + key + ") DO NOTHING")
		if err != nil || len(statements) != 1 {
			t.Fatal(key, statements, err)
		}
		insert := statements[0].(*Insert)
		aggregate, ok := insert.Conflict.Keys[0].Expression.(AggregateExpr)
		if !ok || len(aggregate.Args) != 1 || aggregate.Pos.Character == 0 {
			t.Fatal(key, insert.Conflict.Keys[0])
		}
		if strings.HasPrefix(key, "pg_catalog.") && (aggregate.Schema == nil || aggregate.Schema.Text != "pg_catalog") {
			t.Fatal("schema lost", aggregate)
		}
	}
	for _, key := range []string{"other.sum(k)", "abs(k)", "sum(VARIADIC k)", "sum(k=>1)"} {
		statements, err := Parse("INSERT INTO t VALUES(1) ON CONFLICT(" + key + ") DO NOTHING")
		if err != nil || len(statements) != 1 {
			t.Fatal(key, statements, err)
		}
		if _, ok := statements[0].(*Insert).Conflict.Keys[0].Expression.(FunctionExpr); !ok {
			t.Fatal("generic call was reclassified", key, statements[0])
		}
	}
	statements, err := Parse("CREATE INDEX i ON t(sum(k))")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	if _, ok := statements[0].(*CreateIndex).Keys[0].Expression.(FunctionExpr); !ok {
		t.Fatal("CREATE call classification changed", statements[0])
	}
	call := FunctionExpr{Name: ObjectName{Parts: []Identifier{{Text: "sum"}}}, ResolutionOnly: true}
	keys := insertConflictKeys([]IndexKey{{Expression: call}})
	if _, ok := keys[0].Expression.(FunctionExpr); !ok {
		t.Fatal("failure-only resolution call reclassified", keys[0])
	}
}
