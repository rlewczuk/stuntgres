package control

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// The same empty-ACL constructor used by regobj's lossless value renderer,
// with both argument types qualified for administrative catalog replay.
const emptyObjectACLExpression = `pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)`

// ObjectACLRepair contains only literal destination identities, never captured
// source OIDs or authority. It is admitted only by isolated native Restore.
type ObjectACLRepair struct {
	Class ids.OID
	Identity, Text string
	Absent, Owner bool
}

func repairCast(raw parser.Expr, name string, array bool) (parser.Expr, bool) {
	cast, ok := raw.(parser.CastExpr)
	if !ok { return nil, false }
	t := cast.TypeName
	if t.Name != name || t.Schema == nil || t.Schema.Text != "pg_catalog" || len(t.Modifiers) != 0 || t.Setof || array && len(t.ArrayBounds) != 1 || !array && len(t.ArrayBounds) != 0 { return nil, false }
	return cast.Child,true
}

func repairLiteral(raw parser.Expr, name string, array bool) (parser.Value, bool) {
	child,valid:=repairCast(raw,name,array)
	literal,ok:=child.(parser.LiteralExpr)
	return literal.Value,valid&&ok
}

// Recognize the fixed empty value, never evaluate a function from Restore SQL.
func repairEmptyObjectACL(raw parser.Expr) bool {
	child,ok:=repairCast(raw,"aclitem",true)
	if !ok {return false}
	call,ok:=child.(parser.FunctionExpr)
	if !ok || len(call.Name.Parts)!=2 || call.Name.Parts[0].Text!="pg_catalog" || call.Name.Parts[1].Text!="acldefault" || len(call.Args)!=2 || len(call.ArgNames)!=0 || call.Variadic || call.Window!=nil || call.Star || call.Distinct || call.All || len(call.Order)!=0 || call.Filter!=nil || call.Within || len(call.ColumnDefinitions)!=0 || call.Syntax!=parser.OrdinaryFunctionSyntax {return false}
	code,valid:=repairLiteral(call.Args[0],"char",false)
	if !valid || code.Kind!=parser.StringValue || code.Text!="c" {return false}
	oid,valid:=repairLiteral(call.Args[1],"oid",false)
	return valid && oid.Kind==parser.IntegerValue && oid.Integer==0
}

// ParseObjectACLRepair recognizes exactly the generated one-assignment shape.
func ParseObjectACLRepair(s *parser.Update) (r ObjectACLRepair, ok bool) {
	if s == nil || len(s.Name.Parts) != 2 || s.Name.Parts[0].Text != "pg_catalog" || len(s.With) != 0 || s.WithRecursive || s.Only || s.Star || s.Alias.Text != "" || s.From != nil || s.CurrentOf != nil || s.Returning != nil || len(s.Assignments) != 1 { return }
	var column, key, typ string
	switch s.Name.Parts[1].Text {
	case "pg_proc": r.Class, column, key, typ = ids.ProcedureClassID, "proacl", "oid", "regprocedure"
	case "pg_language": r.Class, column, key, typ = ids.LanguageClassID, "lanacl", "lanname", "name"
	case "pg_foreign_data_wrapper": r.Class, column, key, typ = ids.ForeignWrapperClassID, "fdwacl", "fdwname", "name"
	case "pg_foreign_server": r.Class, column, key, typ = ids.ForeignServerClassID, "srvacl", "srvname", "name"
	default: return
	}
	a := s.Assignments[0]
	if len(a.Steps) != 0 || len(a.Targets) != 0 { return }
	if r.Class == ids.LanguageClassID && a.Target.Text == "lanowner" { r.Owner = true } else if a.Target.Text != column { return }
	valueType, array := "aclitem", true
	if r.Owner { valueType, array = "regrole", false }
	v, valid := repairLiteral(a.Value, valueType, array)
	if valid && v.Kind==parser.StringValue && !r.Owner && v.Text=="{}" {return}
	if !r.Owner && repairEmptyObjectACL(a.Value) {
		v,valid=parser.Value{Kind:parser.StringValue,Text:"{}"},true
	}
	if !valid || v.Kind != parser.StringValue && (r.Owner || v.Kind != parser.NullValue) { return }
	predicate, valid := s.Where.(parser.OperatorExpr)
	if !valid || len(predicate.Name.Parts) != 1 || predicate.Name.Parts[0].Text != "=" || len(predicate.Args) != 2 { return }
	col, valid := predicate.Args[0].(parser.ColumnExpr)
	if !valid || len(col.Name.Parts) != 1 || col.Name.Parts[0].Text != key { return }
	identity, valid := repairLiteral(predicate.Args[1], typ, false)
	if !valid || identity.Kind != parser.StringValue { return }
	r.Identity, r.Text, r.Absent = identity.Text, v.Text, v.Kind == parser.NullValue
	return r, true
}
