package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

const VerifyHeapamSignature catalog.ProcedureSignature = "2205,16,16,25,20,20"

func verifyHeapamDeclaration() catalog.InstalledRoutineDeclaration {
	return catalog.InstalledRoutineDeclaration{Parallel: 'u', ReturnsSet: true, Rows: 1000, Defaults: 5}
}

type AmcheckProvider struct{ control catalog.ExtensionControl }

var StockAmcheck = &AmcheckProvider{control: catalog.ExtensionControl{
	Version: "1.5", Superuser: true, Relocatable: true,
	Comment: new("functions for verifying relation integrity"),
}}

func (p *AmcheckProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *AmcheckProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.5"}}
}
func AmcheckProviderOf(p *catalog.Procedure) *AmcheckProvider {
	provider, _ := p.Implementation.(*AmcheckProvider)
	return provider
}
func (p *AmcheckProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	if p != StockAmcheck || v.Owner() == nil || !currentManifest(v, ext) || ext.Name() != "amcheck" || ext.Version != "1.5" || len(ext.Members) != 1 {
		return fmt.Errorf("stuntgres: invalid amcheck manifest")
	}
	proc, ok := v.Procedure(ext.Members[0])
	if !ok { return fmt.Errorf("stuntgres: missing amcheck member") }
	_, err := ProcedureSymbol(v, proc)
	return err
}
func (p *AmcheckProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	ext, ok := v.Extension(proc.Extension)
	if !ok { return fmt.Errorf("stuntgres: missing amcheck installation") }
	if err := p.ValidateExtension(v, ext); err != nil { return err }
	if v.Get(proc.Address()) != proc || ext.Members[0] != proc.Address() {
		return fmt.Errorf("stuntgres: foreign amcheck procedure")
	}
	return nil
}
func ValidateVerifyHeapamIdentity(v catalog.Read, proc *catalog.Procedure, ref ids.ObjectRef) error {
	actual, ok := v.Procedure(ref.Address)
	if v.Owner() == nil || !ok || proc == nil || actual != proc || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || AmcheckProviderOf(proc) != StockAmcheck {
		return fmt.Errorf("stuntgres: invalid stored verify_heapam identity")
	}
	return StockAmcheck.ValidateProcedure(v, proc)
}
func (p *AmcheckProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	oid, err := stage.AllocateOID()
	if err != nil { return err }
	revision, err := stage.Revision()
	if err != nil { return err }
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
	proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "verify_heapam", Signature: VerifyHeapamSignature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 2249, Volatility: expr.Volatile, Installed: verifyHeapamDeclaration(), Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "verify_heapam"}, Implementation: p,
		ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege}}},
	}
	if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
	}
	if err := stage.Create(proc); err != nil { return err }
	ext.Members = []ids.ObjectAddress{address}
	return nil
}
