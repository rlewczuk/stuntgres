package engine

import (
	"stuntgres/internal/parser"
)

func restoreNNName(parts ...string) parser.ObjectName {
	name := parser.ObjectName{}
	for _, part := range parts {
		name.Parts = append(name.Parts, parser.Identifier{Text: part})
	}
	return name
}

func restoreNNText(text string) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text}}
}

func restoreNNEqual(column string, value parser.Expr) parser.Expr {
	return parser.OperatorExpr{Name: restoreNNName("="), Args: []parser.Expr{parser.ColumnExpr{Name: restoreNNName(column)}, value}}
}

func restoreNNRegclass(text string) parser.Expr {
	return parser.RegclassExpr{Child: restoreNNText(text)}
}

func restoreNNRaw(relation, column, name string) *parser.Update {
	query := &parser.Select{Targets: []parser.Expr{parser.ColumnExpr{Name: restoreNNName("attnum")}}, From: &parser.TableReference{Name: restoreNNName("pg_catalog", "pg_attribute")}, Where: parser.LogicalExpr{Op: parser.LogicalAnd, Left: restoreNNEqual("attrelid", restoreNNRegclass(relation)), Right: restoreNNEqual("attname", restoreNNText(column))}}
	return &parser.Update{Name: restoreNNName("pg_catalog", "pg_constraint"), Assignments: []parser.UpdateAssignment{{Target: parser.Identifier{Text: "connoinherit"}, Value: parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}}}, Where: parser.LogicalExpr{Op: parser.LogicalAnd, Left: parser.LogicalExpr{Op: parser.LogicalAnd, Left: restoreNNEqual("conrelid", restoreNNRegclass(relation)), Right: restoreNNEqual("conname", restoreNNText(name))}, Right: restoreNNEqual("conkey", parser.ArrayExpr{Elements: []parser.Expr{parser.SubqueryExpr{Kind: parser.SubqueryScalar, Query: query}}})}}
}
