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
	ext.Members = make([]ids.ObjectAddress, len(functions.PgcryptoMembers))
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
		ext.Members[i] = address
		proc := &procedureDescriptor{address: address, namespace: ext.TargetNamespace, Extension: ext.address, revision: revision, name: member.Name, Signature: procedureSignature(member.Signature), RoutineKind: functionRoutine, Owner: owner, Result: member.Result, Volatility: member.Volatility, Strict: member.Strict, Implementation: p}
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
	if v.owner == nil || p != stockPgcrypto || ext.name != "pgcrypto" || ext.Version != p.control.version || len(ext.Members) != len(functions.PgcryptoMembers) {
		return invalid()
	}
	seen := make(map[functions.PgcryptoMember]bool, len(functions.PgcryptoMembers))
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok {
			return invalid()
		}
		member, admitted := functions.PgcryptoLookup(proc.name, string(proc.Signature))
		if !admitted || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.address != address || proc.namespace != ext.TargetNamespace || proc.Extension != ext.address || proc.revision == 0 || proc.Implementation != p || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) || proc.Result != member.Result || proc.Volatility != member.Volatility || proc.Strict != member.Strict || seen[member] || v.NameAddress(descriptorNameKey(proc)) != address {
			return invalid()
		}
		// Extension membership fixes provider semantics, not independently
		// transferable member ownership. Each owner still has real provenance.
		if proc.Owner.ID() == 0 || proc.Owner != v.owner.principals.Principal(proc.Owner.ID()) {
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
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing pgcrypto installation")
	}
	return p.ValidateExtension(v, ext)
}
