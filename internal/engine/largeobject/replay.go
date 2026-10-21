package largeobject

import "stuntgres/internal/parser"

func qualified(name parser.ObjectName, last string) bool {
	return len(name.Parts) == 2 && name.Parts[0].Text == "pg_catalog" && name.Parts[1].Text == last
}
func castLiteral(raw parser.Expr, name string, array bool) (parser.Value, bool) {
	cast, ok := raw.(parser.CastExpr)
	if !ok {
		return parser.Value{}, false
	}
	t := cast.TypeName
	if t.Name != name || t.Schema == nil || t.Schema.Text != "pg_catalog" || len(t.Modifiers) != 0 || t.Setof || (array && len(t.ArrayBounds) != 1) || (!array && len(t.ArrayBounds) != 0) {
		return parser.Value{}, false
	}
	literal, ok := cast.Child.(parser.LiteralExpr)
	return literal.Value, ok
}
func oidLiteral(raw parser.Expr) (uint32, bool) {
	v, ok := castLiteral(raw, "oid", false)
	return uint32(v.Integer), ok && v.Kind == parser.IntegerValue && v.Integer > 0 && v.Integer <= 4294967295
}

// RestoresBytes admits only the fixed-OID literal create and bounded literal
// put emitted by Dump. No parameters, subqueries, labels or arbitrary functions.
func RestoresBytes(s *parser.Select) bool {
	if s == nil || len(s.With) != 0 || s.WithRecursive || s.From != nil || s.Values != nil || s.Where != nil || s.GroupBy != nil || s.Having != nil || len(s.Windows) != 0 || s.Order != nil || s.Into != nil || len(s.Locking) != 0 || s.Bounds != nil || s.Distinct != nil || s.Set != nil || s.Parenthesized || len(s.Targets) != 1 {
		return false
	}
	for _, label := range s.Labels {
		if label.Text != "" {
			return false
		}
	}
	call, ok := s.Targets[0].(parser.FunctionExpr)
	if !ok || len(call.ArgNames) != 0 || call.Variadic || call.Window != nil || call.Star || call.Distinct || call.All || len(call.Order) != 0 || call.Filter != nil {
		return false
	}
	if qualified(call.Name, "lo_create") && len(call.Args) == 1 {
		_, ok := oidLiteral(call.Args[0])
		return ok
	}
	if !qualified(call.Name, "lo_put") || len(call.Args) != 3 {
		return false
	}
	if _, ok := oidLiteral(call.Args[0]); !ok {
		return false
	}
	offset, ok := castLiteral(call.Args[1], "int8", false)
	if !ok || offset.Kind != parser.IntegerValue || offset.Integer < 0 || offset.Integer > MaximumSize {
		return false
	}
	decode, ok := call.Args[2].(parser.FunctionExpr)
	if !ok || !qualified(decode.Name, "decode") || len(decode.Args) != 2 || len(decode.ArgNames) != 0 || decode.Variadic || decode.Window != nil || decode.Star || decode.Distinct || decode.All || len(decode.Order) != 0 || decode.Filter != nil {
		return false
	}
	data, ok := decode.Args[0].(parser.LiteralExpr)
	format, fok := decode.Args[1].(parser.LiteralExpr)
	if !ok || !fok || data.Value.Kind != parser.StringValue || format.Value.Kind != parser.StringValue || format.Value.Text != "hex" || len(data.Value.Text) == 0 || len(data.Value.Text) > int(PageSize)*2 || len(data.Value.Text)%2 != 0 {
		return false
	}
	for _, c := range data.Value.Text {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return int64(len(data.Value.Text)/2) <= MaximumSize-offset.Integer
}

// ACLRepair recognizes exactly one generated typed raw ACL replacement.
func ACLRepair(s *parser.Update) (oid uint32, text string, absent bool, ok bool) {
	if s == nil || !qualified(s.Name, "pg_largeobject_metadata") || len(s.With) != 0 || s.WithRecursive || s.Only || s.Star || s.Alias.Text != "" || s.From != nil || s.CurrentOf != nil || s.Returning != nil || len(s.Assignments) != 1 {
		return
	}
	a := s.Assignments[0]
	if a.Target.Text != "lomacl" || len(a.Steps) != 0 || len(a.Targets) != 0 {
		return
	}
	v, valid := castLiteral(a.Value, "aclitem", true)
	if !valid || v.Kind != parser.StringValue && v.Kind != parser.NullValue {
		return
	}
	predicate, valid := s.Where.(parser.OperatorExpr)
	if !valid || len(predicate.Name.Parts) != 1 || predicate.Name.Parts[0].Text != "=" || len(predicate.Args) != 2 {
		return
	}
	column, valid := predicate.Args[0].(parser.ColumnExpr)
	if !valid || len(column.Name.Parts) != 1 || column.Name.Parts[0].Text != "oid" {
		return
	}
	oid, valid = oidLiteral(predicate.Args[1])
	if !valid {
		return
	}
	return oid, v.Text, v.Kind == parser.NullValue, true
}
