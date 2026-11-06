package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type PgPrewarmProvider struct{ control catalog.ExtensionControl }
var StockPgPrewarm = &PgPrewarmProvider{control: catalog.ExtensionControl{Version: "1.2", Superuser: true, Relocatable: true, Comment: new("prewarm relation data")}}
const PgPrewarmSignature catalog.ProcedureSignature = "2205,25,25,20,20"
var pgPrewarmDeclaration = catalog.InstalledRoutineDeclaration{Parallel: 's', Defaults: 4}
var pgPrewarmInputNames = catalog.NewRoutineInputNames("", "mode", "fork", "first_block", "last_block")
var pgPrewarmDefaults = catalog.NewRoutineLiteralDefaults(
	catalog.RoutineLiteral{SourceType: 25, Text: "buffer"},
	catalog.RoutineLiteral{SourceType: 25, Text: "main"},
	catalog.RoutineLiteral{SourceType: 20, Null: true},
	catalog.RoutineLiteral{SourceType: 20, Null: true},
)
const pgPrewarmInstallContext = "SQL statement \"CREATE FUNCTION pg_prewarm(regclass,\n\t\t\t\t\t\t   mode text default 'buffer',\n\t\t\t\t\t\t   fork text default 'main',\n\t\t\t\t\t\t   first_block int8 default null,\n\t\t\t\t\t\t   last_block int8 default null)\nRETURNS int8\nAS '$libdir/pg_prewarm', 'pg_prewarm'\nLANGUAGE C PARALLEL SAFE\"\nextension script file \"pg_prewarm--1.1.sql\", near line 7"

func (p *PgPrewarmProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*PgPrewarmProvider) Versions() catalog.ExtensionVersions { return catalog.ExtensionVersions{Bases: []string{"1.2"}} }
func PgPrewarmProviderOf(p *catalog.Procedure) *PgPrewarmProvider {
	if p == nil { return nil }
	provider, _ := p.Implementation.(*PgPrewarmProvider)
	return provider
}
func (p *PgPrewarmProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	if p != StockPgPrewarm || v.Owner() == nil || !currentManifest(v, ext) || ext.Name() != "pg_prewarm" || ext.Version != "1.2" || len(ext.Members) != 1 { return fmt.Errorf("stuntgres: invalid pg_prewarm manifest") }
	member, ok := v.Procedure(ext.Members[0])
	if !ok { return fmt.Errorf("stuntgres: missing pg_prewarm member") }
	_, err := ProcedureSymbol(v, member)
	return err
}
func (p *PgPrewarmProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error { return ValidateCurrentInstallation(v, e, p) }
func (p *PgPrewarmProvider) ValidateProcedure(v catalog.Read, r *catalog.Procedure) error { return ValidateProcedureOrigin(v, r, p) }
func ValidatePgPrewarmIdentity(v catalog.Read, p *catalog.Procedure, ref ids.ObjectRef) error {
	current, ok := v.Procedure(ref.Address)
	if v.Owner() == nil || !ok || p == nil || current != p || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || PgPrewarmProviderOf(p) != StockPgPrewarm { return fmt.Errorf("stuntgres: invalid stored pg_prewarm identity") }
	return StockPgPrewarm.ValidateProcedure(v, p)
}
func (p *PgPrewarmProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	oid, err := stage.AllocateOID()
	if err != nil { return err }
	revision, err := stage.Revision()
	if err != nil { return err }
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
	member := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "pg_prewarm", Signature: PgPrewarmSignature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 20, Volatility: expr.Volatile, Installed: pgPrewarmDeclaration, InputNames: pgPrewarmInputNames, LiteralDefaults: pgPrewarmDefaults, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "pg_prewarm", Binary: "$libdir/pg_prewarm", HasBinary: true}, Implementation: p}
	if stage.LookupName(catalog.DescriptorNameKey(member)) != (ids.ObjectAddress{}) { return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: "function \"pg_prewarm\" already exists with same argument types", Context: pgPrewarmInstallContext} }
	member.ACL = stage.CreationRoutineACL(owner, ext.TargetNamespace)
	if err := stage.Create(member); err != nil { return err }
	// No script GRANT/REVOKE: ordinary creation ACL is not initial history.
	ext.Members = append(ext.Members, address)
	return nil
}
func ValidatePgPrewarmCall(v catalog.Read, p *catalog.Procedure, call functions.PgPrewarm) error {
	if p == nil || PgPrewarmProviderOf(p) != StockPgPrewarm || call.RoutineReference != p { return fmt.Errorf("stuntgres: invalid pg_prewarm call identity") }
	if _, err := ProcedureSymbol(v, p); err != nil { return err }
	if err := call.ValidateArguments(call.Args); err != nil { return err }
	typ, ok := v.LookupType(sqltype.Int8)
	if !ok || call.ResultUsage() != typ.Usage() { return fmt.Errorf("stuntgres: invalid pg_prewarm result") }
	return nil
}
