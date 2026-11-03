package extension

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (p *PgStatStatementsProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	ext.Members = make([]ids.ObjectAddress, 0, 2)
	for slot, name := range []string{"pg_stat_statements", "pg_stat_statements_info"} {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		signature := catalog.ProcedureSignature("16")
		symbol := "pg_stat_statements_1_12"
		if slot == 1 {
			signature = ""
			symbol = name
		}
		procedure := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: signature, Owner: owner, RoutineKind: catalog.FunctionRoutine, Result: 2249, Volatility: expr.Volatile, Strict: true, Installed: pgStatStatementsDeclaration(slot), Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: symbol, Binary: "$libdir/pg_stat_statements", HasBinary: true}, Implementation: p}
		procedure.ACL = stage.CreationRoutineACL(owner, ext.TargetNamespace)
		if stage.LookupName(catalog.DescriptorNameKey(procedure)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", name)}
		}
		if err := stage.Create(procedure); err != nil {
			return err
		}
		ext.Members = append(ext.Members, procedure.Address())
	}
	return nil
}
func (*PgStatStatementsProvider) ConsumerViews(version string) ([]ConsumerView, error) {
	if version != "1.12" {
		return nil, fmt.Errorf("stuntgres: unsupported pg_stat_statements consumer release %q", version)
	}
	declarations := make([]ConsumerView, 2)
	for i, name := range []string{"pg_stat_statements", "pg_stat_statements_info"} {
		call := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}}
		if i == 0 {
			call.Args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}}
		}
		declarations[i] = ConsumerView{Statement: &parser.CreateView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Query: &parser.Select{Targets: []parser.Expr{parser.StarExpr{}}, From: &parser.FunctionTable{Calls: []parser.FunctionExpr{call}}}}, ACL: []ConsumerViewACL{{Public: true, Privileges: authorization.SelectPrivilege}}}
	}
	return declarations, nil
}
