package control

import "stuntgres/internal/parser"

func originQualified(name parser.ObjectName, last string) bool {
	return len(name.Parts) == 2 && name.Parts[0].Text == "pg_catalog" && name.Parts[1].Text == last
}
func originString(raw parser.Expr) bool {
	value, ok := raw.(parser.LiteralExpr)
	return ok && value.Value.Kind == parser.StringValue
}
func originColumn(raw parser.Expr, name string) bool {
	column, ok := raw.(parser.ColumnExpr)
	return ok && len(column.Name.Parts) == 1 && column.Name.Parts[0].Text == name
}
func originCast(raw parser.Expr, name string) (parser.Expr, bool) {
	cast, ok := raw.(parser.CastExpr)
	if !ok {
		return nil, false
	}
	typ := cast.TypeName
	return cast.Child, typ.Name == name && typ.Schema != nil && typ.Schema.Text == "pg_catalog" && len(typ.Modifiers) == 0 && !typ.Setof && len(typ.ArrayBounds) == 0
}

// RestoresReplicationOrigin admits only emitted literal standalone state or an
// exact canonical-name expression over one named subscription. No generic SELECT,
// parameters, labels, subqueries, extra expressions or arbitrary calls enter.
func RestoresReplicationOrigin(s *parser.Select) bool {
	if s == nil || s.RoutineReturn || len(s.With) != 0 || s.WithRecursive || s.Values != nil || s.GroupBy != nil || s.Having != nil || len(s.Windows) != 0 || s.Order != nil || s.Into != nil || len(s.Locking) != 0 || s.Bounds != nil || s.Distinct != nil || s.Set != nil || s.Parenthesized || len(s.Targets) != 1 {
		return false
	}
	for _, label := range s.Labels {
		if label.Text != "" {
			return false
		}
	}
	call, ok := s.Targets[0].(parser.FunctionExpr)
	if !ok || call.ProcedureCall || len(call.ArgNames) != 0 || call.Variadic || call.Window != nil || call.Star || call.Distinct || call.All || len(call.Order) != 0 || call.Filter != nil || call.Within || len(call.ColumnDefinitions) != 0 {
		return false
	}
	create := originQualified(call.Name, "pg_replication_origin_create")
	drop := originQualified(call.Name, "pg_replication_origin_drop")
	advance := originQualified(call.Name, "pg_replication_origin_advance")
	if !(create || drop || advance) || len(call.Args) != 1 && !advance || advance && len(call.Args) != 2 {
		return false
	}
	if advance {
		child, ok := originCast(call.Args[1], "pg_lsn")
		if !ok || !originString(child) {
			return false
		}
	}
	if s.From == nil {
		return s.Where == nil && originString(call.Args[0])
	}
	if create {
		return false
	}
	table, ok := s.From.(*parser.TableReference)
	if !ok || table.Only || !originQualified(table.Name, "pg_subscription") {
		return false
	}
	predicate, ok := s.Where.(parser.OperatorExpr)
	if !ok || len(predicate.Name.Parts) != 1 || predicate.Name.Parts[0].Text != "=" || len(predicate.Args) != 2 || !originColumn(predicate.Args[0], "subname") || !originString(predicate.Args[1]) {
		return false
	}
	concat, ok := call.Args[0].(parser.OperatorExpr)
	if !ok || len(concat.Name.Parts) != 1 || concat.Name.Parts[0].Text != "||" || len(concat.Args) != 2 {
		return false
	}
	prefix, ok := concat.Args[0].(parser.LiteralExpr)
	if !ok || prefix.Value.Kind != parser.StringValue || prefix.Value.Text != "pg_" {
		return false
	}
	oid, ok := originCast(concat.Args[1], "text")
	return ok && originColumn(oid, "oid")
}
