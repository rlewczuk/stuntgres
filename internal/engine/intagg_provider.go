package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

type intaggProvider struct{ control extensionControl }

// Control implements extensionImplementation.
func (p *intaggProvider) Control() extensionControl { return p.control.detached() }

var stockIntagg = &intaggProvider{control: extensionControl{Version: "1.1", Superuser: true, Relocatable: true, Comment: new("integer aggregator and enumerator (obsolete)")}}

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
	ext.Members = make([]ids.ObjectAddress, len(intaggMembers))
	for i, member := range intaggMembers {
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		revision, err := e.catalog.revisionLocked()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		ext.Members[i] = address
		proc := &procedureDescriptor{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.name, Signature: member.signature, RoutineKind: member.kind, Owner: owner, Result: member.result, Volatility: expr.Immutable, Strict: member.strict, Implementation: p}
		if i == 0 {
			state, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			final, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			proc.Aggregate = &ownedAggregate{
				Definition: intaggDefinition(oid, state, final),
				Helpers: [2]routineIdentity{
					{state, ext.TargetNamespace.ObjectID, "int_agg_state", "2281,23"},
					{final, ext.TargetNamespace.ObjectID, "int_agg_final_array", "2281"},
				},
			}
		}
		if stage.lookupName(descriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return nil
}

func (p *intaggProvider) ValidateExtension(v CatalogRead, ext *extensionDescriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid intagg manifest") }
	if p != stockIntagg || v.Owner() == nil || ext.Name() != "intagg" || ext.Version != p.control.Version || len(ext.Members) != len(intaggMembers) {
		return invalid()
	}
	var seen [len(intaggMembers)]bool
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.Address() != address || proc.Namespace() != ext.TargetNamespace || proc.Extension != ext.Address() || proc.Revision() == 0 || proc.Volatility != expr.Immutable || proc.Implementation != p || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) || v.NameAddress(descriptorNameKey(proc)) != address {
			return invalid()
		}
		if err := validateOwnedAggregate(v, proc); err != nil {
			return err
		}
		found := false
		for j, member := range intaggMembers {
			if proc.Name() == member.name && proc.Signature == member.signature && proc.Result == member.result && proc.Strict == member.strict && proc.RoutineKind == member.kind {
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
		if proc.Owner.ID() == 0 || proc.Owner != v.Owner().Principals.Principal(proc.Owner.ID()) {
			return invalid()
		}
	}
	return nil
}

func (p *intaggProvider) validateProcedure(v CatalogRead, proc *procedureDescriptor) error {
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing intagg installation")
	}
	if err := p.ValidateExtension(v, ext); err != nil {
		return err
	}
	if v.Get(proc.Address()) != proc || proc.intaggProvider() != p {
		return fmt.Errorf("stuntgres: foreign intagg procedure")
	}
	for _, address := range ext.Members {
		if address == proc.Address() {
			return nil
		}
	}
	return fmt.Errorf("stuntgres: nonmember intagg procedure")
}
