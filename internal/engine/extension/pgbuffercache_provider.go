package extension

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type PgBuffercacheProvider struct{ control catalog.ExtensionControl }

var StockPgBuffercache = &PgBuffercacheProvider{control: catalog.ExtensionControl{Version: "1.6", Superuser: true, Relocatable: true, Comment: new("examine the shared buffer cache")}}
var pgBuffercacheDeclaration = catalog.InstalledRoutineDeclaration{Parallel: 's', ReturnsSet: true, Rows: 1000}

const pgBuffercacheFunctionContext = "SQL statement \"CREATE FUNCTION pg_buffercache_pages()\nRETURNS SETOF RECORD\nAS '$libdir/pg_buffercache', 'pg_buffercache_pages'\nLANGUAGE C PARALLEL SAFE\"\nextension script file \"pg_buffercache--1.2.sql\", near line 7"
const pgBuffercacheViewContext = "SQL statement \"CREATE VIEW pg_buffercache AS\n\tSELECT P.* FROM pg_buffercache_pages() AS P\n\t(bufferid integer, relfilenode oid, reltablespace oid, reldatabase oid,\n\t relforknumber int2, relblocknumber int8, isdirty bool, usagecount int2,\n\t pinning_backends int4)\"\nextension script file \"pg_buffercache--1.2.sql\", near line 13"

func (p *PgBuffercacheProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*PgBuffercacheProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.6"}}
}
func PgBuffercacheProviderOf(p *catalog.Procedure) *PgBuffercacheProvider {
	if p == nil {
		return nil
	}
	provider, _ := p.Implementation.(*PgBuffercacheProvider)
	return provider
}
func (p *PgBuffercacheProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *PgBuffercacheProvider) ValidateProcedure(v catalog.Read, r *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, r, p)
}
func ValidatePgBuffercacheIdentity(v catalog.Read, p *catalog.Procedure, ref ids.ObjectRef) error {
	current, ok := v.Procedure(ref.Address)
	if v.Owner() == nil || !ok || p == nil || current != p || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || PgBuffercacheProviderOf(p) != StockPgBuffercache {
		return fmt.Errorf("stuntgres: invalid stored pg_buffercache identity")
	}
	return StockPgBuffercache.ValidateProcedure(v, p)
}
func (p *PgBuffercacheProvider) validateRecipe(v catalog.Read, e *catalog.Extension) error {
	if p != StockPgBuffercache || !currentManifest(v, e) || e.Name() != "pg_buffercache" || e.Version != "1.6" || len(e.Members) != 6 {
		return fmt.Errorf("stuntgres: invalid pg_buffercache manifest")
	}
	for _, address := range e.Members {
		procedure, ok := v.Procedure(address)
		if !ok {
			return fmt.Errorf("stuntgres: missing pg_buffercache declaration")
		}
		if err := p.ValidateProcedure(v, procedure); err != nil {
			return err
		}
	}
	return nil
}
func (provider *PgBuffercacheProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	monitor := stage.CurrentRead().Owner().Principals.Principal(3373) // PostgreSQL's fixed pg_monitor identity.
	if _, err := stage.Roles().Descriptor(monitor); err != nil {
		return err
	}
	oid, err := stage.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := stage.Revision()
	if err != nil {
		return err
	}
	p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "pg_buffercache_pages", RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 2249, Volatility: expr.Volatile, Installed: pgBuffercacheDeclaration, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "pg_buffercache_pages", Binary: "$libdir/pg_buffercache", HasBinary: true}, Implementation: provider}
	if stage.LookupName(catalog.DescriptorNameKey(p)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: "function \"pg_buffercache_pages\" already exists with same argument types", Context: pgBuffercacheFunctionContext}
	}
	p.ACL, err = stage.RoutineCreationACLWithoutPublic(owner, ext.TargetNamespace)
	if err != nil {
		return err
	}
	p.ACL, _, err = authorization.ChangeObjectACL(context.Background(), stage.Roles(), p.ACL, owner, owner, []authorization.ACLGrantee{{Role: monitor}}, authorization.ExecutePrivilege, false, false, false, false)
	if err != nil {
		return err
	}
	if err := stage.Create(p); err != nil {
		return err
	}
	stage.RecordInitialPrivilege(p.Address(), 'e', p.ACL)
	ext.Members = append(ext.Members, p.Address())
	if err := provider.stageEvictions(stage, ext, owner); err != nil {
		return err
	}
	return provider.stageAggregates(stage, ext, owner)
}
func (*PgBuffercacheProvider) ConsumerViews(version string) ([]ConsumerView, error) {
	if version != "1.6" {
		return nil, fmt.Errorf("stuntgres: unsupported pg_buffercache consumer release %q", version)
	}
	names := [...]string{"bufferid", "relfilenode", "reltablespace", "reldatabase", "relforknumber", "relblocknumber", "isdirty", "usagecount", "pinning_backends"}
	types := [...]string{"integer", "oid", "oid", "oid", "smallint", "bigint", "boolean", "smallint", "integer"}
	definitions := make([]parser.FunctionColumnDefinition, len(names))
	for i, name := range names {
		definitions[i] = parser.FunctionColumnDefinition{Name: parser.Identifier{Text: name}, Type: parser.TypeName{Name: types[i]}}
	}
	call := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_buffercache_pages"}}}}
	from := &parser.AliasReference{Input: &parser.FunctionTable{Calls: []parser.FunctionExpr{call}}, Name: parser.Identifier{Text: "p"}, ColumnDefinitions: definitions}
	view := &parser.CreateView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_buffercache"}}}, Query: &parser.Select{Targets: []parser.Expr{parser.StarExpr{}}, From: from}}
	all := authorization.SelectPrivilege | authorization.InsertPrivilege | authorization.UpdatePrivilege | authorization.DeletePrivilege | authorization.TruncatePrivilege | authorization.ReferencesPrivilege | authorization.TriggerPrivilege | authorization.MaintainPrivilege
	return []ConsumerView{{Statement: view, InstallContext: pgBuffercacheViewContext, ACL: []ConsumerViewACL{{Public: true, Privileges: all, Revoke: true}, {Role: 3373, Privileges: authorization.SelectPrivilege}}}}, nil
}
