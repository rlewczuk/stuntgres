package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

type intaggProvider struct{ version string }

var stockIntagg = &intaggProvider{version: "1.1"}

var intaggMembers = [...]struct {
	name      string
	signature procedureSignature
	result    ids.OID
	strict    bool
	kind      routineKind
}{
	{"int_array_aggregate", "23", 1007, false, aggregateRoutine},
	{"int_array_enum", "1007", 23, true, functionRoutine},
}

func (p *intaggProvider) stage(e *Engine, stage *CatalogStage, ext *extensionDescriptor, owner authorization.PrincipalRef, auth AuthorizationView) error {
	ext.members = make([]ids.ObjectAddress, len(intaggMembers))
	for i, member := range intaggMembers {
		oid, err := e.allocateOIDLocked()
		if err != nil {
			return err
		}
		revision, err := e.revisionLocked()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		ext.members[i] = address
		proc := &procedureDescriptor{address: address, namespace: ext.targetNamespace, extension: ext.address, revision: revision, name: member.name, signature: member.signature, routineKind: member.kind, owner: owner, result: member.result, volatility: Immutable, strict: member.strict, intagg: p}
		if i == 0 {
			state, err := e.allocateOIDLocked()
			if err != nil {
				return err
			}
			final, err := e.allocateOIDLocked()
			if err != nil {
				return err
			}
			proc.aggregate = &ownedAggregate{
				definition: intaggDefinition(oid, state, final),
				helpers: [2]routineIdentity{
					{state, ext.targetNamespace.ObjectID, "int_agg_state", "2281,23"},
					{final, ext.targetNamespace.ObjectID, "int_agg_final_array", "2281"},
				},
			}
		}
		if stage.lookupName(descriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.name)}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return nil
}

func (p *intaggProvider) validate(v CatalogRead, ext *extensionDescriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid intagg manifest") }
	if p != stockIntagg || v.owner == nil || ext.name != "intagg" || ext.version != p.version || len(ext.members) != len(intaggMembers) {
		return invalid()
	}
	var seen [len(intaggMembers)]bool
	for _, address := range ext.members {
		proc, ok := v.state.descriptors[address].(*procedureDescriptor)
		if !ok || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.address != address || proc.namespace != ext.targetNamespace || proc.extension != ext.address || proc.revision == 0 || proc.volatility != Immutable || proc.intagg != p || proc.uuidOssp != nil || proc.fuzzy != nil || proc.crypto != nil || proc.provider != nil || proc.queryCodec != nil || proc.conversion.Name() != "" || proc.dictionary != (ids.ObjectAddress{}) || v.state.names[descriptorNameKey(proc)] != address {
			return invalid()
		}
		if err := validateOwnedAggregate(v, proc); err != nil {
			return err
		}
		found := false
		for j, member := range intaggMembers {
			if proc.name == member.name && proc.signature == member.signature && proc.result == member.result && proc.strict == member.strict && proc.routineKind == member.kind {
				if seen[j] {
					return invalid()
				}
				seen[j], found = true, true
				break
			}
		}
		if !found {
			return invalid()
		}
		if proc.owner.ID() == 0 || proc.owner != v.owner.authorizationDomain.Principal(proc.owner.ID()) {
			return invalid()
		}
	}
	return nil
}

func (p *intaggProvider) validateProcedure(v CatalogRead, proc *procedureDescriptor) error {
	ext, ok := v.state.descriptors[proc.extension].(*extensionDescriptor)
	if !ok {
		return fmt.Errorf("stuntgres: missing intagg installation")
	}
	if err := p.validate(v, ext); err != nil {
		return err
	}
	if v.state.descriptors[proc.address] != proc || proc.intagg != p {
		return fmt.Errorf("stuntgres: foreign intagg procedure")
	}
	for _, address := range ext.members {
		if address == proc.address {
			return nil
		}
	}
	return fmt.Errorf("stuntgres: nonmember intagg procedure")
}
