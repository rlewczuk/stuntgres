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

// PgsurgeryProvider owns the finite, freeze-only installed cohort.
type PgsurgeryProvider struct{ control catalog.ExtensionControl }

var StockPgsurgery = &PgsurgeryProvider{control: catalog.ExtensionControl{
	Version: "1.0", Superuser: true, Relocatable: true,
	Comment: new("extension to perform surgery on a damaged relation"),
}}

var pgsurgeryDeclaration = catalog.InstalledRoutineDeclaration{Parallel: 'u', ArgumentNames: [4]string{"reloid", "tids"}}

func (p *PgsurgeryProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*PgsurgeryProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.0"}}
}
func PgsurgeryProviderOf(p *catalog.Procedure) *PgsurgeryProvider {
	if p == nil {
		return nil
	}
	provider, _ := p.Implementation.(*PgsurgeryProvider)
	return provider
}
func (p *PgsurgeryProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	if p != StockPgsurgery || v.Owner() == nil || !currentManifest(v, ext) || ext.Name() != "pg_surgery" || ext.Version != "1.0" || len(ext.Members) != 1 {
		return fmt.Errorf("stuntgres: invalid pg_surgery manifest")
	}
	proc, ok := v.Procedure(ext.Members[0])
	if !ok {
		return fmt.Errorf("stuntgres: missing pg_surgery member")
	}
	_, err := ProcedureSymbol(v, proc)
	return err
}
func (p *PgsurgeryProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	return ValidateCurrentInstallation(v, ext, p)
}
func (p *PgsurgeryProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, proc, p)
}
func ValidatePgsurgeryIdentity(v catalog.Read, proc *catalog.Procedure, ref ids.ObjectRef) error {
	if v.Owner() == nil || proc == nil || v.Get(ref.Address) != proc || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || PgsurgeryProviderOf(proc) != StockPgsurgery {
		return fmt.Errorf("stuntgres: invalid stored pg_surgery identity")
	}
	return StockPgsurgery.ValidateProcedure(v, proc)
}
func ValidatePgsurgeryCall(v catalog.Read, proc *catalog.Procedure, call functions.Pgsurgery) error {
	symbol, err := ProcedureSymbol(v, proc)
	if err != nil {
		return err
	}
	result, ok := v.LookupType(sqltype.Void)
	if !ok || PgsurgeryProviderOf(proc) != StockPgsurgery || symbol != "heap_force_freeze" || call.CallRoutineReference() != proc || call.ResultUsage() != result.Usage() {
		return fmt.Errorf("stuntgres: invalid installed pg_surgery call")
	}
	return call.ValidateArguments(call.Args)
}
func (p *PgsurgeryProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	oid, err := stage.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := stage.Revision()
	if err != nil {
		return err
	}
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
	proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "heap_force_freeze", Signature: "2205,1010", RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 2278, Volatility: expr.Volatile, Strict: true, Installed: pgsurgeryDeclaration, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "heap_force_freeze", Binary: "$libdir/pg_surgery", HasBinary: true}, Implementation: p}
	if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
	}
	proc.ACL, err = stage.RoutineCreationACLWithoutPublic(owner, ext.TargetNamespace)
	if err != nil {
		return err
	}
	if err := stage.Create(proc); err != nil {
		return err
	}
	stage.RecordInitialPrivilege(address, 'e', proc.ACL)
	ext.Members = append(ext.Members, address)
	return nil
}
