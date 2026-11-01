package extension

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type PgvisibilityProvider struct{ control catalog.ExtensionControl }

var StockPgvisibility = &PgvisibilityProvider{control: catalog.ExtensionControl{Version: "1.2", Superuser: true, Relocatable: true, Comment: new("examine the visibility map (VM) and page-level visibility info")}}

func (p *PgvisibilityProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*PgvisibilityProvider) Versions() catalog.ExtensionVersions { return catalog.ExtensionVersions{Bases: []string{"1.2"}} }
func PgvisibilityProviderOf(p *catalog.Procedure) *PgvisibilityProvider {
	if p == nil { return nil }
	provider, _ := p.Implementation.(*PgvisibilityProvider)
	return provider
}

// Declaration order and OUT objects are immutable released installation authority.
var PgvisibilityMembers = [...]struct {
	Name, Source string
	Signature catalog.ProcedureSignature
	Kind functions.PgvisibilityKind
	Declaration catalog.InstalledRoutineDeclaration
}{
	{"pg_visibility_map", "pg_visibility_map", "2205,20", functions.VisibilityMapBlock, catalog.InstalledRoutineDeclaration{Parallel: 'u', Outputs: catalog.NewRoutineOutputs([]ids.OID{16,16}, "", "blkno", "all_visible", "all_frozen")}},
	{"pg_visibility", "pg_visibility", "2205,20", functions.VisibilityPageBlock, catalog.InstalledRoutineDeclaration{Parallel: 'u', Outputs: catalog.NewRoutineOutputs([]ids.OID{16,16,16}, "", "blkno", "all_visible", "all_frozen", "pd_all_visible")}},
	{"pg_visibility_map", "pg_visibility_map_rel", "2205", functions.VisibilityMapRelation, catalog.InstalledRoutineDeclaration{Parallel: 'u', ReturnsSet: true, Rows: 1000, Outputs: catalog.NewRoutineOutputs([]ids.OID{20,16,16}, "", "blkno", "all_visible", "all_frozen")}},
	{"pg_visibility", "pg_visibility_rel", "2205", functions.VisibilityPageRelation, catalog.InstalledRoutineDeclaration{Parallel: 'u', ReturnsSet: true, Rows: 1000, Outputs: catalog.NewRoutineOutputs([]ids.OID{20,16,16,16}, "", "blkno", "all_visible", "all_frozen", "pd_all_visible")}},
	{"pg_visibility_map_summary", "pg_visibility_map_summary", "2205", functions.VisibilityMapSummary, catalog.InstalledRoutineDeclaration{Parallel: 'u', Outputs: catalog.NewRoutineOutputs([]ids.OID{20,20}, "", "all_visible", "all_frozen")}},
}

func (p *PgvisibilityProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	if p != StockPgvisibility || v.Owner() == nil || !currentManifest(v, ext) || ext.Name() != "pg_visibility" || ext.Version != "1.2" || len(ext.Members) != len(PgvisibilityMembers) { return fmt.Errorf("stuntgres: invalid pg_visibility manifest") }
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok { return fmt.Errorf("stuntgres: missing pg_visibility member") }
		if _, err := ProcedureSymbol(v, proc); err != nil { return err }
	}
	return nil
}
func (p *PgvisibilityProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error { return ValidateCurrentInstallation(v, ext, p) }
func (p *PgvisibilityProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error { return ValidateProcedureOrigin(v, proc, p) }
func ValidatePgvisibilityIdentity(v catalog.Read, proc *catalog.Procedure, ref ids.ObjectRef) error {
	actual, ok := v.Procedure(ref.Address)
	if v.Owner() == nil || !ok || proc == nil || actual != proc || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || PgvisibilityProviderOf(proc) != StockPgvisibility { return fmt.Errorf("stuntgres: invalid stored pg_visibility identity") }
	return StockPgvisibility.ValidateProcedure(v, proc)
}
func (p *PgvisibilityProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	roles := stage.Roles()
	scan := stage.CurrentRead().Owner().Principals.Principal(authorization.StatScanTablesRoleID)
	if _, err := roles.Descriptor(scan); err != nil { return err }
	for _, member := range PgvisibilityMembers {
		oid, err := stage.AllocateOID(); if err != nil { return err }
		revision, err := stage.Revision(); if err != nil { return err }
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.Name, Signature: member.Signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 2249, Volatility: expr.Volatile, Strict: true, Installed: member.Declaration, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: member.Source, Binary: "$libdir/pg_visibility", HasBinary: true}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) { return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())} }
		proc.ACL, err = stage.RoutineCreationACLWithoutPublic(owner, ext.TargetNamespace); if err != nil { return err }
		proc.ACL, _, err = authorization.ChangeObjectACL(context.Background(), roles, proc.ACL, owner, owner, []authorization.ACLGrantee{{Role: scan}}, authorization.ExecutePrivilege, false, false, false, false)
		if err != nil { return err }
		if err := stage.Create(proc); err != nil { return err }
		stage.RecordInitialPrivilege(address, 'e', proc.ACL)
		ext.Members = append(ext.Members, address)
	}
	return nil
}

func ValidatePgvisibilityCall(v catalog.Read, proc *catalog.Procedure, call functions.Pgvisibility) error {
	if proc == nil || PgvisibilityProviderOf(proc) != StockPgvisibility || call.RoutineReference != proc { return fmt.Errorf("stuntgres: invalid pg_visibility call identity") }
	if _, err := ProcedureSymbol(v, proc); err != nil { return err }
	member := PgvisibilityMembers[proc.Origin.Declaration]
	if call.Kind != member.Kind { return fmt.Errorf("stuntgres: mismatched pg_visibility overload") }
	if err := call.ValidateArguments(call.Args); err != nil { return err }
	d, outputs := call.ResultUsage().Type, proc.OutputDeclaration()
	if d == nil || outputs == nil || d.CompositeLen() != outputs.Len() { return fmt.Errorf("stuntgres: invalid pg_visibility record") }
	for i := range outputs.Len() {
		field := d.CompositeMember(i)
		typ, ok := v.LookupType(sqltype.ID(outputs.Type(i)))
		if !ok || field.Attribute != int32(i+1) || field.Name != outputs.Name(outputs.Inputs()+i) || field.Usage != typ.Usage() { return fmt.Errorf("stuntgres: invalid pg_visibility OUT field") }
	}
	return nil
}
