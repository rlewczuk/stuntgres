package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

type uuidOsspProvider struct{ control catalog.ExtensionControl }

// Control implements extensionImplementation.
func (p *uuidOsspProvider) Control() catalog.ExtensionControl { return p.control.Detached() }

var stockUUIDOssp = &uuidOsspProvider{control: catalog.ExtensionControl{Version: "1.1", Trusted: true, Superuser: true, Relocatable: true, Comment: new("generate universally unique identifiers (UUIDs)")}}

func (p *uuidOsspProvider) stage(e *Engine, stage *CatalogStage, ext *catalog.Extension, owner authorization.PrincipalRef, auth AuthorizationView) error {
	ext.Members = make([]ids.ObjectAddress, 0, len(functions.UUIDOsspMembers))
	for _, member := range functions.UUIDOsspMembers {
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		revision, err := e.catalog.revisionLocked()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.Name, Signature: "", Owner: owner, RoutineKind: catalog.FunctionRoutine, Result: 2950, Volatility: member.Volatility, Strict: true, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
		ext.Members = append(ext.Members, address)
	}
	return nil
}

func (p *uuidOsspProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid uuid-ossp manifest") }
	if p != stockUUIDOssp || ext.Name() != "uuid-ossp" || ext.Version != p.control.Version || len(ext.Members) != len(functions.UUIDOsspMembers) {
		return invalid()
	}
	var seen [len(functions.UUIDOsspMembers)]bool
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok {
			return invalid()
		}
		memberIndex := -1
		for i, member := range functions.UUIDOsspMembers {
			if proc.Name() == member.Name && proc.Volatility == member.Volatility {
				memberIndex = i
				break
			}
		}
		if memberIndex < 0 || seen[memberIndex] || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.Address() != address || proc.Namespace() != ext.TargetNamespace || proc.Extension != ext.Address() || proc.Revision() == 0 || proc.RoutineKind != catalog.FunctionRoutine || proc.Builtin != nil || proc.Implementation != p || proc.Aggregate != nil || proc.Foreign != nil || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) || proc.Signature != "" || proc.Result != 2950 || !proc.Strict || proc.Owner == (authorization.PrincipalRef{}) || v.NameAddress(catalog.DescriptorNameKey(proc)) != address {
			return invalid()
		}
		seen[memberIndex] = true
	}
	return nil
}

func (p *uuidOsspProvider) validateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	if v.Get(proc.Address()) != proc || uuidOsspProviderOf(proc) != p {
		return fmt.Errorf("stuntgres: invalid uuid-ossp procedure identity")
	}
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing uuid-ossp installation")
	}
	return p.ValidateExtension(v, ext)
}
