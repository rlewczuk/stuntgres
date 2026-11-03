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

// PgsurgeryProvider owns the finite freeze-first, kill-appended installed cohort.
type PgsurgeryProvider struct{ control catalog.ExtensionControl }

var StockPgsurgery = &PgsurgeryProvider{control: catalog.ExtensionControl{
	Version: "1.0", Superuser: true, Relocatable: true,
	Comment: new("extension to perform surgery on a damaged relation"),
}}

var pgsurgeryDeclaration = catalog.InstalledRoutineDeclaration{Parallel: 'u', ArgumentNames: [4]string{"reloid", "tids"}}
var pgsurgeryMembers = [...]string{"heap_force_freeze", "heap_force_kill"}

// The released script declares kill before freeze, independently of the
// native cohort's preserved identity-allocation order.
var pgsurgeryScriptContexts = [...]string{
	"SQL statement \"CREATE FUNCTION heap_force_freeze(reloid regclass, tids tid[])\nRETURNS VOID\nAS '$libdir/pg_surgery', 'heap_force_freeze'\nLANGUAGE C STRICT\"\nextension script file \"pg_surgery--1.0.sql\", near line 13",
	"SQL statement \"CREATE FUNCTION heap_force_kill(reloid regclass, tids tid[])\nRETURNS VOID\nAS '$libdir/pg_surgery', 'heap_force_kill'\nLANGUAGE C STRICT\"\nextension script file \"pg_surgery--1.0.sql\", near line 6",
}

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
	if p != StockPgsurgery || v.Owner() == nil || !currentManifest(v, ext) || ext.Name() != "pg_surgery" || ext.Version != "1.0" || len(ext.Members) != len(pgsurgeryMembers) {
		return fmt.Errorf("stuntgres: invalid pg_surgery manifest")
	}
	for _, member := range ext.Members {
		proc, ok := v.Procedure(member)
		if !ok {
			return fmt.Errorf("stuntgres: missing pg_surgery member")
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	return nil
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
	if !ok || PgsurgeryProviderOf(proc) != StockPgsurgery || call.Operation.Symbol() == "" || symbol != call.Operation.Symbol() || call.CallRoutineReference() != proc || call.ResultUsage() != result.Usage() {
		return fmt.Errorf("stuntgres: invalid installed pg_surgery call")
	}
	return call.ValidateArguments(call.Args)
}
func (p *PgsurgeryProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	for slot, name := range pgsurgeryMembers {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: "2205,1010", RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 2278, Volatility: expr.Volatile, Strict: true, Installed: pgsurgeryDeclaration, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/pg_surgery", HasBinary: true}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name()), Context: pgsurgeryScriptContexts[slot]}
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
	}
	return nil
}
