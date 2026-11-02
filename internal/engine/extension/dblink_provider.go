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

// DblinkProvider installs the released declaration for the implemented SQL
// constructor. It does not install connection or adjacent SQL-builder stubs.
type DblinkProvider struct{ control catalog.ExtensionControl }

var StockDblink = &DblinkProvider{control: catalog.ExtensionControl{Version: "1.2", Superuser: true, Relocatable: true, Comment: new("connect to other PostgreSQL databases from within a database")}}

func (p *DblinkProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*DblinkProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.2"}}
}
func DblinkProviderOf(p *catalog.Procedure) *DblinkProvider {
	if p == nil { return nil }
	provider, _ := p.Implementation.(*DblinkProvider)
	return provider
}
func (p *DblinkProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	if p != StockDblink || !currentManifest(v, ext) || ext.Name() != "dblink" || ext.Version != "1.2" || len(ext.Members) != 1 {
		return fmt.Errorf("stuntgres: invalid dblink manifest")
	}
	member, ok := v.Procedure(ext.Members[0])
	if !ok { return fmt.Errorf("stuntgres: missing dblink member") }
	_, err := ProcedureSymbol(v, member)
	return err
}
func (p *DblinkProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	return ValidateCurrentInstallation(v, ext, p)
}
func (p *DblinkProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, proc, p)
}
func (p *DblinkProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	oid, err := stage.AllocateOID()
	if err != nil { return err }
	revision, err := stage.Revision()
	if err != nil { return err }
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
	proc := &catalog.Procedure{
		ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision,
		ObjectName: functions.DblinkDeleteName, Signature: functions.DblinkDeleteSignature, Owner: owner,
		RoutineKind: catalog.FunctionRoutine, Result: 25, Volatility: expr.Volatile, Strict: true,
		Installed: catalog.InstalledRoutineDeclaration{Parallel: 'r'},
		Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: functions.DblinkDeleteName, Binary: "$libdir/dblink", HasBinary: true}, Implementation: p,
	}
	if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
	}
	if err := stage.Create(proc); err != nil { return err }
	ext.Members = []ids.ObjectAddress{address}
	return nil
}
func ValidateDblinkIdentity(v catalog.Read, p *catalog.Procedure, ref ids.ObjectRef) error {
	current, ok := v.Procedure(ref.Address)
	if v.Owner() == nil || !ok || p == nil || current != p || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || DblinkProviderOf(p) != StockDblink {
		return fmt.Errorf("stuntgres: invalid stored dblink identity")
	}
	return StockDblink.ValidateProcedure(v, p)
}
func ValidateDblinkBuildSQLDeleteCall(v catalog.Read, p *catalog.Procedure, call functions.DblinkBuildSQLDelete) error {
	if p == nil || DblinkProviderOf(p) != StockDblink || call.RoutineReference != p {
		return fmt.Errorf("stuntgres: invalid dblink constructor identity")
	}
	symbol, err := ProcedureSymbol(v, p)
	if err != nil { return err }
	if symbol != functions.DblinkDeleteName || p.Signature != functions.DblinkDeleteSignature {
		return fmt.Errorf("stuntgres: mismatched dblink constructor")
	}
	if err := call.ValidateArguments(call.Args[:]); err != nil { return err }
	typ, ok := v.LookupType(sqltype.Text)
	if !ok || call.ResultUsage() != typ.Usage() { return fmt.Errorf("stuntgres: invalid dblink constructor result") }
	return nil
}
