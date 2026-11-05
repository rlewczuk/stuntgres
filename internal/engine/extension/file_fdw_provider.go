package extension

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

type FileFDWProvider struct { control catalog.ExtensionControl }

var StockFileFDW = &FileFDWProvider{control: catalog.ExtensionControl{Version: "1.0", Superuser: true, Relocatable: true, Comment: new("foreign-data wrapper for flat file access")}}
var fileFDWHandler = &catalog.ForeignProcedure{Wrapper: StockFileFDW}
var fileFDWValidator = &catalog.ForeignProcedure{}

func (p *FileFDWProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *FileFDWProvider) Versions() catalog.ExtensionVersions { return catalog.ExtensionVersions{Bases: []string{"1.0"}} }
func (p *FileFDWProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error { return ValidateCurrentInstallation(v, e, p) }
func (p *FileFDWProvider) ValidateProcedure(v catalog.Read, d *catalog.Procedure) error { return ValidateProcedureOrigin(v, d, p) }
func (p *FileFDWProvider) SupportsValidator(d *catalog.Procedure) bool {
	return p == StockFileFDW && d != nil && d.Implementation == p && d.Foreign == fileFDWValidator
}
func (p *FileFDWProvider) SupportsHandler(d *catalog.Procedure) bool {
	return p == StockFileFDW && d != nil && d.Implementation == p && d.Foreign == fileFDWHandler
}
func (p *FileFDWProvider) validateRecipe(v catalog.Read, e *catalog.Extension) error {
	if p != StockFileFDW || !currentManifest(v, e) || e.Name() != "file_fdw" || e.Version != "1.0" || len(e.Members) != 3 { return InvalidProviderDescriptor() }
	return validateFileFDWRecipe(v, e.Installation)
}

func fileFDWDeclaration(slot int) (string, catalog.ProcedureSignature, ids.OID, *catalog.ForeignProcedure) {
	if slot == 0 { return "file_fdw_handler", catalog.ForeignHandlerSignature, 3115, fileFDWHandler }
	return "file_fdw_validator", catalog.ForeignValidatorSignature, 2278, fileFDWValidator
}

func fileFDWProcedureShape(d *catalog.Procedure, slot int) bool {
	if d == nil || slot < 0 || slot > 1 { return false }
	name, signature, result, token := fileFDWDeclaration(slot)
	return d.Implementation == StockFileFDW && d.Foreign == token && d.Signature == signature && d.Result == result && d.RoutineKind == catalog.FunctionRoutine && d.Strict && d.Volatility == expr.Volatile &&
		d.Installed == (catalog.InstalledRoutineDeclaration{Parallel: 'u'}) && d.Display == (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/file_fdw", HasBinary: true}) &&
		d.SQL == nil && d.Builtin == nil && d.Aggregate == nil && d.SQLBody == nil && d.SQLDefaults == nil && d.DeclarationBody == nil && d.LiteralDefaults == nil && d.InputNames == nil && d.RangeConstructor == nil && d.Host == nil && d.HostHandler == nil && d.HostConversion == nil && d.Sepgsql == nil && d.PLHandler == catalog.NoPLHandler && d.AccessMethod == catalog.NoAccessMethod && d.BrinSupport == 0 && d.GinSupport == 0 && d.Conversion.Name() == "" && d.Dictionary == (ids.ObjectAddress{}) && d.Language == (ids.ObjectAddress{}) && d.SnowballHelper == catalog.NoSnowballHelper && !d.SecurityDefiner && d.Configuration == nil && len(d.Body) == 0 && len(d.DeclarationDependencies) == 0 && d.Transforms == nil
}

func validateFileFDWRecipe(v catalog.Read, installation *catalog.Installation) error {
	if !installation.Valid() || installation.Provider != "file_fdw" || installation.Version != "1.0" || installation.Implementation != StockFileFDW || v.Owner() == nil || v.Owner().Extensions["file_fdw"] != StockFileFDW || len(installation.Members) != 3 { return InvalidProviderDescriptor() }
	for slot := range 2 {
		a := installation.Members[slot]
		canonical, ok := installation.Declarations[slot].(*catalog.Procedure)
		name, _, _, _ := fileFDWDeclaration(slot)
		if !ok || a.ClassID != ids.ProcedureClassID || a.SubID != 0 || !fileFDWProcedureShape(canonical, slot) || canonical.Name() != name || canonical.Address() != a || canonical.Origin != nil { return InvalidProviderDescriptor() }
		if d := v.Get(a); d != nil {
			live, ok := d.(*catalog.Procedure)
			if !ok || !fileFDWProcedureShape(live, slot) || live.Revision() == 0 || !live.Origin.Valid(a) || live.Origin.Installation != installation || live.Origin.Declaration != slot || live.Owner.ID() == 0 || live.Owner != v.Owner().Principals.Principal(live.Owner.ID()) || v.NameAddress(catalog.DescriptorNameKey(live)) != a { return InvalidProviderDescriptor() }
			if _, exists := v.Namespace(live.Namespace()); !exists { return InvalidProviderDescriptor() }
			if err := catalog.ValidateExtensionMembership(v, live); err != nil { return err }
		}
	}
	canonical, ok := installation.Declarations[2].(*catalog.ForeignObject)
	a := installation.Members[2]
	if !ok || canonical == nil || a.ClassID != ids.ForeignWrapperClassID || a.SubID != 0 || canonical.Address() != a || canonical.Name() != "file_fdw" || canonical.Origin != nil || !fileFDWWrapperShape(canonical, installation) { return InvalidProviderDescriptor() }
	if d := v.Get(a); d != nil {
		live, ok := d.(*catalog.ForeignObject)
		if !ok || live == nil || live.Revision() == 0 || !live.Origin.Valid(a) || live.Origin.Installation != installation || live.Origin.Declaration != 2 || !fileFDWWrapperShape(live, installation) || live.Owner.ID() == 0 || live.Owner != v.Owner().Principals.Principal(live.Owner.ID()) || v.NameAddress(catalog.DescriptorNameKey(live)) != a { return InvalidProviderDescriptor() }
		if err := catalog.ValidateExtensionMembership(v, live); err != nil { return err }
	}
	return nil
}

func fileFDWWrapperShape(d *catalog.ForeignObject, i *catalog.Installation) bool {
	return d.Handler == i.Members[0] && d.Validator == i.Members[1] && len(d.Options) == 0 &&
		d.Wrapper == (ids.ObjectAddress{}) && d.Server == (ids.ObjectAddress{}) &&
		d.MappedRole == (authorization.PrincipalRef{}) && d.MappedName == "" && d.ServerType == nil && d.Version == nil
}

func fileFDWProcedureName(v catalog.Read, d *catalog.Procedure) (string, error) {
	if d == nil || v.Get(d.Address()) != d || d.Address().ClassID != ids.ProcedureClassID || d.Revision() == 0 || !d.Origin.Valid(d.Address()) || d.Origin.Declaration > 1 { return "", InvalidProviderDescriptor() }
	if err := validateFileFDWRecipe(v, d.Origin.Installation); err != nil { return "", err }
	name, _, _, _ := fileFDWDeclaration(d.Origin.Declaration)
	return name, nil
}

func (p *FileFDWProvider) ValidateForeign(v catalog.Read, d *catalog.ForeignObject) error {
	if p != StockFileFDW || d == nil || v.Get(d.Address()) != d || d.Revision() == 0 || !d.Origin.Valid(d.Address()) || d.Origin.Declaration != 2 { return InvalidProviderDescriptor() }
	return validateFileFDWRecipe(v, d.Origin.Installation)
}

func (p *FileFDWProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	members := make([]ids.ObjectAddress, 3)
	for slot := range 3 {
		oid, err := stage.AllocateOID()
		if err != nil { return err }
		revision, err := stage.Revision()
		if err != nil { return err }
		if slot < 2 {
			name, signature, result, token := fileFDWDeclaration(slot)
			a := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
			d := &catalog.Procedure{ObjectAddress: a, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Owner: owner, RoutineKind: catalog.FunctionRoutine, Signature: signature, Result: result, Strict: true, Volatility: expr.Volatile, Implementation: p, Foreign: token, Installed: catalog.InstalledRoutineDeclaration{Parallel: 'u'}, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/file_fdw", HasBinary: true}}
			if stage.LookupName(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
				context := "SQL statement \"CREATE FUNCTION file_fdw_handler()\nRETURNS fdw_handler\nAS '$libdir/file_fdw'\nLANGUAGE C STRICT\"\nextension script file \"file_fdw--1.0.sql\", near line 6"
				if slot == 1 { context = "SQL statement \"CREATE FUNCTION file_fdw_validator(text[], oid)\nRETURNS void\nAS '$libdir/file_fdw'\nLANGUAGE C STRICT\"\nextension script file \"file_fdw--1.0.sql\", near line 11" }
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", name), Context: context}
			}
			if err := stage.Create(d); err != nil { return err }
			members[slot] = a
		} else {
			a := ids.ObjectAddress{ClassID: ids.ForeignWrapperClassID, ObjectID: oid}
			d := &catalog.ForeignObject{ObjectAddress: a, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "file_fdw", Owner: owner, Handler: members[0], Validator: members[1]}
			if stage.LookupName(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) { return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `foreign-data wrapper "file_fdw" already exists`} }
			if err := stage.Create(d); err != nil { return err }
			members[slot] = a
		}
	}
	ext.Members = members
	return nil
}

const FileFDWExternalBoundary = "file_fdw server filesystem and program execution are not supported by the embedded engine"
func fileFDWBoundary(ctx context.Context) error {
	if err := ctx.Err(); err != nil { return err }
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: FileFDWExternalBoundary}
}
func (*FileFDWProvider) GetForeignRelSize(ctx context.Context, _ catalog.ForeignRequest) (catalog.ForeignRelSize, error) { return catalog.ForeignRelSize{}, fileFDWBoundary(ctx) }
func (*FileFDWProvider) GetForeignPaths(ctx context.Context, _ catalog.ForeignRequest, _ catalog.ForeignRelSize) ([]catalog.ForeignPath, error) { return nil, fileFDWBoundary(ctx) }
func (*FileFDWProvider) GetForeignPlan(ctx context.Context, _ catalog.ForeignRequest, _ catalog.ForeignPath) (catalog.ForeignPlan, error) { return catalog.ForeignPlan{}, fileFDWBoundary(ctx) }
func (*FileFDWProvider) BeginForeignScan(ctx context.Context, _ catalog.ForeignRequest, _ catalog.ForeignPlan) (catalog.ForeignScan, error) { return nil, fileFDWBoundary(ctx) }
