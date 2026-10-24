package parser

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestDeleteCoreGrammar(t *testing.T) {
	for _, query := range []string{
		"DELETE FROM t", "DELETE FROM public.t WHERE id=1", "DELETE FROM t AS d WHERE d.id=$1 AND NOT (d.flag OR FALSE)",
		`/*é*/ DELETE FROM "Mixed" "Alias" WHERE "Alias"."Value"<>NULL`,
		"DELETE FROM t AS d WHERE d.id IS NULL OR d.flag IS NOT UNKNOWN",
		"DELETE FROM t AS d WHERE d.id ISNULL OR d.id NOTNULL",
		"DELETE FROM t AS d WHERE d.id IS DISTINCT FROM 1 AND d.flag IS TRUE",
		"DELETE FROM t WHERE CAST(id AS BIGINT)=1",
		"DELETE FROM t WHERE id::BIGINT=1", "DELETE FROM t WHERE int8(id)=1",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		stmt, ok := statements[0].(*Delete)
		if !ok || stmt.Pos.Character <= 0 || len(stmt.Name.Parts) == 0 || stmt.Name.Parts[0].Pos.Character <= stmt.Pos.Character {
			t.Fatal(stmt)
		}
	}
	statements, err := Parse("DELETE FROM t AS d WHERE d.id=1")
	if err != nil {
		t.Fatal(err)
	}
	stmt := statements[0].(*Delete)
	column := stmt.Where.(OperatorExpr).Args[0].(ColumnExpr)
	if stmt.Alias.Text != "d" || stmt.Alias.Pos.Character != 18 || len(column.Name.Parts) != 2 || column.Pos.Character != 26 {
		t.Fatal(stmt, column)
	}
	tierStatements, err := Parse("DELETE FROM t d WHERE d.v=10 IS TRUE AND NOT d.f IS UNKNOWN OR d.v IS NULL")
	if err != nil {
		t.Fatal(err)
	}
	root := tierStatements[0].(*Delete).Where.(LogicalExpr)
	and := root.Left.(LogicalExpr)
	truth := and.Left.(PredicateExpr)
	not := and.Right.(LogicalExpr)
	if root.Op != LogicalOr || and.Op != LogicalAnd || truth.Kind != IsTrue || not.Op != LogicalNot || not.Left.(PredicateExpr).Kind != IsUnknown {
		t.Fatal("DELETE predicate precedence changed", root)
	}
	if _, ok := truth.Left.(OperatorExpr); !ok {
		t.Fatal("comparison did not bind before IS", truth)
	}
	chainStatements, err := Parse("DELETE FROM t WHERE TRUE IS NULL IS TRUE")
	if err != nil {
		t.Fatal(err)
	}
	chain := chainStatements[0].(*Delete).Where.(PredicateExpr)
	if chain.Kind != IsTrue || chain.Left.(PredicateExpr).Kind != IsNull {
		t.Fatal("unary chain did not nest left", chain)
	}
	for _, query := range []string{"DELETE FROM", "DELETE FROM t WHERE", "DELETE FROM t WHERE id=", "DELETE FROM t AS", "DELETE FROM t WHERE id=1 garbage", "DELETE FROM t; nonsense"} {
		if statements, err := Parse(query); err == nil || statements != nil {
			t.Fatal("valid prefix escaped", query, statements, err)
		}
	}
}

func TestDeleteCoreParserBoundaries(t *testing.T) {
	for _, tc := range []struct{ query, code, message, position string }{
		{"DELETE FROM t WHERE", "42601", "syntax error at end of input", "20"},
		{"CREATE TABLE t(v int4 delete)", "42601", `syntax error at or near "delete"`, "23"},
	} {
		statements, err := Parse(tc.query)
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
		var got *sqlstate.Error
		if statements != nil || !errors.As(err, &got) || *got != want {
			t.Fatal(tc.query, statements, err, got, want)
		}
	}
	statements, err := Parse("SELECT v::int4 delete FROM t")
	if err != nil || len(statements) != 1 {
		t.Fatal("postfix cast keyword alias rejected", statements, err)
	}
	aliased := statements[0].(*Select)
	if len(aliased.Targets) != 1 || len(aliased.Labels) != 1 || aliased.Labels[0].Text != "delete" || aliased.Labels[0].Pos.Character != 16 {
		t.Fatal("lost postfix cast target label", aliased)
	}
	cast, ok := aliased.Targets[0].(CastExpr)
	if !ok || cast.Syntax != PostfixCast || cast.TypeName.Name != "integer" || cast.Pos.Character != 9 {
		t.Fatal("lost postfix cast", aliased.Targets[0])
	}
	if column, ok := cast.Child.(ColumnExpr); !ok || len(column.Name.Parts) != 1 || column.Name.Parts[0].Text != "v" {
		t.Fatal("lost postfix cast operand", cast.Child)
	}
	for _, query := range []string{"SELECT t.id FROM t", "SELECT id FROM t d WHERE d.id=1", "SELECT id FROM t WHERE t.id=1"} {
		if statements, err := Parse(query); err != nil || len(statements) != 1 {
			t.Fatal("query qualification admission", query, statements, err)
		}
	}
	for _, query := range []string{"DELETE FROM t WHERE 1 IS DISTINCT FROM 1 IS TRUE"} {
		if statements, err := Parse(query); err == nil || statements != nil {
			t.Fatal("ungrouped DISTINCT escaped", query, statements, err)
		}
	}
	for _, query := range []string{"CREATE TABLE delete(delete INT)", "INSERT INTO delete(delete) VALUES(1)", "SELECT delete FROM delete WHERE delete=1"} {
		if _, err := Parse(query); err != nil {
			t.Fatal("unreserved DELETE category changed", query, err)
		}
	}
}

func TestDeleteCurrentOfGrammar(t *testing.T) {
	for _, query := range []string{
		`DELETE FROM t WHERE CURRENT OF "MiX"`,
		`DELETE FROM t AS d USING s WHERE CURRENT OF "MiX" RETURNING d.id,s.id`,
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		stmt := statements[0].(*Delete)
		if stmt.CurrentOf == nil || stmt.CurrentOf.Text != "MiX" || stmt.CurrentOf.Pos.Character <= stmt.Pos.Character || stmt.Where != nil {
			t.Fatal("positioned qualifier lost identity or became a predicate", stmt)
		}
	}
	for _, query := range []string{
		"DELETE FROM t WHERE CURRENT OF c AND TRUE",
		"DELETE FROM t WHERE CURRENT OF c OR FALSE",
		"DELETE FROM t WHERE CURRENT OF c WHERE TRUE",
		"DELETE FROM t WHERE CURRENT OF c; nonsense",
		"DELETE FROM t WHERE CURRENT OF $1",
	} {
		if statements, err := Parse(query); err == nil || statements != nil {
			t.Fatal("invalid qualifier escaped", query, statements, err)
		}
	}
}
