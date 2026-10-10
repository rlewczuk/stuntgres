package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestStringAggregateGrammar(t *testing.T) {
	for _, q := range []string{
		"SELECT string_agg(v,d) FROM t",
		"SELECT string_agg(),string_agg(*),string_agg(v),string_agg(v,d,k) FROM t",
		"SELECT pg_catalog.string_agg(v,d ORDER BY k DESC NULLS LAST,id ASC NULLS FIRST) FROM t",
		"SELECT \"string_agg\"(v,d ORDER BY 1,id) FROM t",
		"SELECT string_agg(nextval('s')::text,',' ORDER BY id) FROM t",
		"SELECT string_agg(v,d ORDER BY count(*)) FROM t",
		"SELECT v FROM t LIMIT length(string_agg(v,d ORDER BY id))",
		"INSERT INTO t(v) VALUES(string_agg('a',',' ORDER BY 1))",
		"UPDATE t SET v='x' RETURNING string_agg(v,d ORDER BY id)",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
	for _, q := range []string{
		"SELECT string_agg('x',',')",
		"SELECT COALESCE(string_agg('x',','),nextval('s')::text)",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		if _, ok := statements[0].(*Select); !ok {
			t.Fatal("aggregate did not use shared SELECT", statements)
		}
	}
	statements, err := Parse("SELECT /*é*/ pg_catalog.string_agg(v,d ORDER BY k DESC NULLS LAST,id ASC NULLS FIRST) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	call := statements[0].(*Select).Targets[0].(AggregateExpr)
	if call.Name != "string_agg" || call.Schema == nil || call.Schema.Text != "pg_catalog" || call.Pos.Character != 14 || len(call.Args) != 2 || len(call.Order) != 2 || !call.Order[0].Desc || call.Order[0].Nulls != NullsLast || call.Order[1].Desc || call.Order[1].Nulls != NullsFirst {
		t.Fatal(call)
	}
}

func TestStringAggregateCapabilityGuards(t *testing.T) {
	for _, q := range []string{
		"SELECT string_agg(v,d) OVER () FROM t",
	} {
		statements, err := Parse("INSERT INTO t VALUES('unpublished');" + q)
		var d *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &d) || d.Code != "0A000" {
			t.Fatal(q, statements, err)
		}
	}
}

func TestStringAggregateOrderedKeyVisitors(t *testing.T) {
	key := ColumnExpr{Name: ObjectName{Parts: []Identifier{{Text: "t"}, {Text: "k"}}}, QualificationPos: Position{Offset: 10, Character: 11}}
	call := AggregateExpr{Name: "string_agg", Args: []Expr{LiteralExpr{}, LiteralExpr{}}, Order: []OrderKey{{Expr: key}}}
	if token, _ := nonqueryQualification(call, false); token == nil || token.pos.Character != 11 {
		t.Fatal("ordering qualifier not visited", token)
	}
	marked := groupedQualification(call).(AggregateExpr)
	if !marked.Order[0].Expr.(ColumnExpr).QualificationGrouped {
		t.Fatal("ordered key grouping not retained")
	}
	markedCast := groupedQualification(CastExpr{Child: call}).(CastExpr)
	if !markedCast.Child.(AggregateExpr).Order[0].Expr.(ColumnExpr).QualificationGrouped {
		t.Fatal("ordered key grouping through cast not retained")
	}
}
