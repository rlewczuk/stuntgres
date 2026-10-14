package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
)

// Only the catalog-selected overload is installed, not adjacent fuzzy functions.
type fuzzystrmatchProvider struct{ version string }

var stockFuzzystrmatch = &fuzzystrmatchProvider{version: "1.2"}

func (p *fuzzystrmatchProvider) stage(e *Engine, stage *CatalogStage, ext *extensionDescriptor, owner authorization.PrincipalRef, auth AuthorizationView) error {
	oid, err := e.allocateOIDLocked()
	if err != nil {
		return err
	}
	revision, err := e.revisionLocked()
	if err != nil {
		return err
	}
	address := ObjectAddress{ClassID: ProcedureClassID, ObjectID: oid}
	ext.members = []ObjectAddress{address}
	proc := &procedureDescriptor{address: address, namespace: ext.targetNamespace, extension: ext.address, revision: revision, name: "daitch_mokotoff", signature: unaccentTextSignature, owner: owner, result: 1009, volatility: Immutable, strict: true, fuzzy: p}
	if stage.lookupName(descriptorNameKey(proc)) != (ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: `function "daitch_mokotoff" already exists with same argument types`}
	}
	return stage.Create(proc)
}

func (p *fuzzystrmatchProvider) validate(v CatalogRead, ext *extensionDescriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid fuzzystrmatch manifest") }
	if p != stockFuzzystrmatch || ext.name != "fuzzystrmatch" || ext.version != p.version || len(ext.members) != 1 {
		return invalid()
	}
	address := ext.members[0]
	if address.ClassID != ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 {
		return invalid()
	}
	proc, ok := v.state.descriptors[address].(*procedureDescriptor)
	if !ok || proc.address != address || proc.namespace != ext.targetNamespace || proc.extension != ext.address || proc.revision == 0 || proc.name != "daitch_mokotoff" || proc.signature != unaccentTextSignature || proc.result != 1009 || proc.volatility != Immutable || !proc.strict || proc.fuzzy != p || proc.provider != nil || proc.queryCodec != nil || proc.conversion.Name() != "" || proc.dictionary != (ObjectAddress{}) {
		return invalid()
	}
	return nil
}

func (p *fuzzystrmatchProvider) validateProcedure(v CatalogRead, proc *procedureDescriptor) error {
	ext, ok := v.state.descriptors[proc.extension].(*extensionDescriptor)
	if !ok {
		return fmt.Errorf("stuntgres: missing fuzzystrmatch installation")
	}
	if err := p.validate(v, ext); err != nil {
		return err
	}
	if v.state.descriptors[proc.address] != proc {
		return fmt.Errorf("stuntgres: foreign fuzzystrmatch procedure")
	}
	return nil
}
