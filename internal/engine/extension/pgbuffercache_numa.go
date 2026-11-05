package extension

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

const pgBuffercacheNUMAFunctionContext = "SQL statement \"CREATE OR REPLACE FUNCTION pg_buffercache_numa_pages()\nRETURNS SETOF RECORD\nAS '$libdir/pg_buffercache', 'pg_buffercache_numa_pages'\nLANGUAGE C PARALLEL SAFE\"\nextension script file \"pg_buffercache--1.5--1.6.sql\", near line 7"
const pgBuffercacheNUMAViewContext = "SQL statement \"CREATE VIEW pg_buffercache_numa AS\n\tSELECT P.* FROM pg_buffercache_numa_pages() AS P\n\t(bufferid integer, os_page_num bigint, numa_node integer)\"\nextension script file \"pg_buffercache--1.5--1.6.sql\", near line 13"

func (provider *PgBuffercacheProvider) stageNUMA(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	oid, err := stage.AllocateOID()
	if err != nil { return err }
	revision, err := stage.Revision()
	if err != nil { return err }
	p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "pg_buffercache_numa_pages", RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 2249, Volatility: expr.Volatile, Installed: pgBuffercacheDeclaration, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "pg_buffercache_numa_pages", Binary: "$libdir/pg_buffercache", HasBinary: true}, Implementation: provider}
	if address := stage.LookupName(catalog.DescriptorNameKey(p)); address != (ids.ObjectAddress{}) {
		previous, ok := stage.CurrentRead().Procedure(address)
		if !ok { return fmt.Errorf("stuntgres: existing pg_buffercache NUMA routine has no descriptor") }
		if diagnostic := catalog.CheckRoutineReplacementKind(previous, p.RoutineKind); diagnostic != nil {
			diagnostic.Context = pgBuffercacheNUMAFunctionContext
			return diagnostic
		}
		if diagnostic := catalog.CheckRoutineReplacementResult(previous, p.Result, p.Installed.ReturnsSet, nil); diagnostic != nil {
			diagnostic.Hint = "Use DROP FUNCTION pg_buffercache_numa_pages() first."
			diagnostic.Context = pgBuffercacheNUMAFunctionContext
			return diagnostic
		}
		if previous.Extension != (ids.ObjectAddress{}) {
			incumbent, exists := stage.CurrentRead().Extension(previous.Extension)
			if !exists { return fmt.Errorf("stuntgres: missing incumbent pg_buffercache function extension") }
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: fmt.Sprintf("function pg_buffercache_numa_pages() is already a member of extension %q", incumbent.Name()), Context: pgBuffercacheNUMAFunctionContext}
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "function pg_buffercache_numa_pages() is not a member of extension \"pg_buffercache\"", Detail: "An extension is not allowed to replace an object that it does not own.", Context: pgBuffercacheNUMAFunctionContext}
	}
	p.ACL, err = stage.RoutineCreationACLWithoutPublic(owner, ext.TargetNamespace)
	if err != nil { return err }
	monitor := stage.CurrentRead().Owner().Principals.Principal(3373)
	p.ACL, _, err = authorization.ChangeObjectACL(context.Background(), stage.Roles(), p.ACL, owner, owner, []authorization.ACLGrantee{{Role: monitor}}, authorization.ExecutePrivilege, false, false, false, false)
	if err != nil { return err }
	if err := stage.Create(p); err != nil { return err }
	stage.RecordInitialPrivilege(p.Address(), 'e', p.ACL)
	ext.Members = append(ext.Members, p.Address())
	return nil
}

func pgBuffercacheNUMAView(all authorization.Privileges) ConsumerView {
	names := [...]string{"bufferid", "os_page_num", "numa_node"}
	types := [...]string{"integer", "bigint", "integer"}
	definitions := make([]parser.FunctionColumnDefinition, len(names))
	for i, name := range names {
		definitions[i] = parser.FunctionColumnDefinition{Name: parser.Identifier{Text: name}, Type: parser.TypeName{Name: types[i]}}
	}
	call := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_buffercache_numa_pages"}}}}
	from := &parser.AliasReference{Input: &parser.FunctionTable{Calls: []parser.FunctionExpr{call}}, Name: parser.Identifier{Text: "p"}, ColumnDefinitions: definitions}
	view := &parser.CreateView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_buffercache_numa"}}}, Query: &parser.Select{Targets: []parser.Expr{parser.StarExpr{}}, From: from}}
	return ConsumerView{Statement: view, InstallContext: pgBuffercacheNUMAViewContext, ACL: []ConsumerViewACL{{Public: true, Privileges: all, Revoke: true}, {Role: 3373, Privileges: authorization.SelectPrivilege}}}
}

// PgBuffercacheKindOf accepts only the authenticated anonymous-record slots.
// Eviction and aggregate slots remain separate declared-OUT carrier families.
func PgBuffercacheKindOf(v catalog.Read, p *catalog.Procedure) (functions.PgBuffercacheKind, error) {
	if p == nil || PgBuffercacheProviderOf(p) != StockPgBuffercache {
		return 0, fmt.Errorf("stuntgres: invalid pg_buffercache operation identity")
	}
	if _, err := ProcedureSymbol(v, p); err != nil { return 0, err }
	switch p.Origin.Declaration {
	case 0:
		return functions.PgBuffercachePages, nil
	case 6:
		return functions.PgBuffercacheNUMA, nil
	default:
		return 0, fmt.Errorf("stuntgres: invalid pg_buffercache anonymous declaration")
	}
}

func ValidatePgBuffercacheCall(v catalog.Read, p *catalog.Procedure, call functions.PgBuffercache) error {
	if call.RoutineReference != p { return fmt.Errorf("stuntgres: invalid pg_buffercache routine carrier") }
	kind, err := PgBuffercacheKindOf(v, p)
	if err != nil { return err }
	if call.Kind != kind { return fmt.Errorf("stuntgres: mismatched pg_buffercache operation") }
	return nil
}
