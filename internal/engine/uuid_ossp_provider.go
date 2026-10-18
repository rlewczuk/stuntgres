package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

type uuidOsspProvider struct{ control extensionControl }

// Control implements extensionImplementation.
func (p *uuidOsspProvider) Control() extensionControl { return p.control.Detached() }

var stockUUIDOssp = &uuidOsspProvider{control: extensionControl{Version: "1.1", Trusted: true, Superuser: true, Relocatable: true, Comment: new("generate universally unique identifiers (UUIDs)")}}

func (p *uuidOsspProvider) stage(e *Engine, stage *CatalogStage, ext *extensionDescriptor, owner authorization.PrincipalRef, auth AuthorizationView) error {
	oid, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return err
	}
	revision, err := e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
	proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "uuid_generate_v4", Signature: "", Owner: owner, RoutineKind: catalog.FunctionRoutine, Result: 2950, Volatility: expr.Volatile, Strict: true, Implementation: p}
	if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
	}
	ext.Members = []ids.ObjectAddress{address}
	return stage.Create(proc)
}

func (p *uuidOsspProvider) ValidateExtension(v CatalogRead, ext *extensionDescriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid uuid-ossp manifest") }
	if p != stockUUIDOssp || ext.Name() != "uuid-ossp" || ext.Version != p.control.Version || len(ext.Members) != 1 {
		return invalid()
	}
	address := ext.Members[0]
	proc, ok := v.Procedure(address)
	if !ok || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.Address() != address || proc.Namespace() != ext.TargetNamespace || proc.Extension != ext.Address() || proc.Revision() == 0 || proc.RoutineKind != catalog.FunctionRoutine || proc.Builtin != nil || proc.Implementation != p || proc.Aggregate != nil || proc.Foreign != nil || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) || proc.Name() != "uuid_generate_v4" || proc.Signature != "" || proc.Result != 2950 || proc.Volatility != expr.Volatile || !proc.Strict || proc.Owner == (authorization.PrincipalRef{}) || v.NameAddress(catalog.DescriptorNameKey(proc)) != address {
		return invalid()
	}
	return nil
}

func (p *uuidOsspProvider) validateProcedure(v CatalogRead, proc *catalog.Procedure) error {
	if v.Get(proc.Address()) != proc || uuidOsspProviderOf(proc) != p {
		return fmt.Errorf("stuntgres: invalid uuid-ossp procedure identity")
	}
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing uuid-ossp installation")
	}
	return p.ValidateExtension(v, ext)
}
