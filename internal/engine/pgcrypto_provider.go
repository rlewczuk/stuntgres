package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

type pgcryptoProvider struct{ control extensionControl }

// Control implements extensionImplementation.
func (p *pgcryptoProvider) Control() extensionControl { return p.control.detached() }

var stockPgcrypto = &pgcryptoProvider{control: extensionControl{version: "1.4", trusted: true, superuser: true, relocatable: true, comment: new("cryptographic functions")}}

func (p *pgcryptoProvider) stage(e *Engine, stage *CatalogStage, ext *extensionDescriptor, owner authorization.PrincipalRef, auth AuthorizationView) error {
	ext.members = make([]ids.ObjectAddress, len(functions.PgcryptoMembers))
	for i, member := range functions.PgcryptoMembers {
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		revision, err := e.catalog.revisionLocked()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		ext.members[i] = address
		proc := &procedureDescriptor{address: address, namespace: ext.targetNamespace, extension: ext.address, revision: revision, name: member.Name, signature: procedureSignature(member.Signature), routineKind: functionRoutine, owner: owner, result: member.Result, volatility: member.Volatility, strict: member.Strict, implementation: p}
		if stage.lookupName(descriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.name)}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return nil
}

func (p *pgcryptoProvider) ValidateExtension(v CatalogRead, ext *extensionDescriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid pgcrypto manifest") }
	if v.owner == nil || p != stockPgcrypto || ext.name != "pgcrypto" || ext.version != p.control.version || len(ext.members) != len(functions.PgcryptoMembers) {
		return invalid()
	}
	seen := make(map[functions.PgcryptoMember]bool, len(functions.PgcryptoMembers))
	for _, address := range ext.members {
		proc, ok := v.Procedure(address)
		if !ok {
			return invalid()
		}
		member, admitted := functions.PgcryptoLookup(proc.name, string(proc.signature))
		if !admitted || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.address != address || proc.namespace != ext.targetNamespace || proc.extension != ext.address || proc.revision == 0 || proc.implementation != p || proc.conversion.Name() != "" || proc.dictionary != (ids.ObjectAddress{}) || proc.result != member.Result || proc.volatility != member.Volatility || proc.strict != member.Strict || seen[member] || v.NameAddress(descriptorNameKey(proc)) != address {
			return invalid()
		}
		// Extension membership fixes provider semantics, not independently
		// transferable member ownership. Each owner still has real provenance.
		if proc.owner.ID() == 0 || proc.owner != v.owner.principals.Principal(proc.owner.ID()) {
			return invalid()
		}
		seen[member] = true
	}
	return nil
}

func (p *pgcryptoProvider) validateProcedure(v CatalogRead, proc *procedureDescriptor) error {
	if v.Get(proc.address) != proc || proc.pgcryptoProvider() != p {
		return fmt.Errorf("stuntgres: invalid pgcrypto procedure identity")
	}
	ext, ok := v.Extension(proc.extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing pgcrypto installation")
	}
	return p.ValidateExtension(v, ext)
}
