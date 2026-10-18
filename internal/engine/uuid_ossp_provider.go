package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

type uuidOsspProvider struct{ version string }

var stockUUIDOssp = &uuidOsspProvider{version: "1.1"}

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
	proc := &procedureDescriptor{address: address, namespace: ext.targetNamespace, extension: ext.address, revision: revision, name: "uuid_generate_v4", signature: "", owner: owner, routineKind: functionRoutine, result: 2950, volatility: Volatile, strict: true, uuidOssp: p}
	if stage.lookupName(descriptorNameKey(proc)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.name)}
	}
	ext.members = []ids.ObjectAddress{address}
	return stage.Create(proc)
}

func (p *uuidOsspProvider) validate(v CatalogRead, ext *extensionDescriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid uuid-ossp manifest") }
	if p != stockUUIDOssp || ext.name != "uuid-ossp" || ext.version != p.version || len(ext.members) != 1 {
		return invalid()
	}
	address := ext.members[0]
	proc, ok := v.state.descriptors[address].(*procedureDescriptor)
	if !ok || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.address != address || proc.namespace != ext.targetNamespace || proc.extension != ext.address || proc.revision == 0 || proc.routineKind != functionRoutine || proc.builtin != nil || proc.uuidOssp != p || proc.intagg != nil || proc.aggregate != nil || proc.foreign != nil || proc.crypto != nil || proc.fuzzy != nil || proc.provider != nil || proc.queryCodec != nil || proc.conversion.Name() != "" || proc.dictionary != (ids.ObjectAddress{}) || proc.name != "uuid_generate_v4" || proc.signature != "" || proc.result != 2950 || proc.volatility != Volatile || !proc.strict || proc.owner == (authorization.PrincipalRef{}) || v.state.names[descriptorNameKey(proc)] != address {
		return invalid()
	}
	return nil
}

func (p *uuidOsspProvider) validateProcedure(v CatalogRead, proc *procedureDescriptor) error {
	if v.state.descriptors[proc.address] != proc || proc.uuidOssp != p {
		return fmt.Errorf("stuntgres: invalid uuid-ossp procedure identity")
	}
	ext, ok := v.state.descriptors[proc.extension].(*extensionDescriptor)
	if !ok {
		return fmt.Errorf("stuntgres: missing uuid-ossp installation")
	}
	return p.validate(v, ext)
}
