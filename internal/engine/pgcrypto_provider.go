package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

type pgcryptoProvider struct{ version string }

var stockPgcrypto = &pgcryptoProvider{version: "1.4"}

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
		proc := &procedureDescriptor{address: address, namespace: ext.targetNamespace, extension: ext.address, revision: revision, name: member.Name, signature: procedureSignature(member.Signature), routineKind: functionRoutine, owner: owner, result: member.Result, volatility: member.Volatility, strict: member.Strict, crypto: p}
		if stage.lookupName(descriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.name)}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return nil
}

func (p *pgcryptoProvider) validate(v CatalogRead, ext *extensionDescriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid pgcrypto manifest") }
	if v.owner == nil || p != stockPgcrypto || ext.name != "pgcrypto" || ext.version != p.version || len(ext.members) != len(functions.PgcryptoMembers) {
		return invalid()
	}
	seen := make(map[functions.PgcryptoMember]bool, len(functions.PgcryptoMembers))
	for _, address := range ext.members {
		proc, ok := v.state.descriptors[address].(*procedureDescriptor)
		if !ok {
			return invalid()
		}
		member, admitted := functions.PgcryptoLookup(proc.name, string(proc.signature))
		if !admitted || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.address != address || proc.namespace != ext.targetNamespace || proc.extension != ext.address || proc.revision == 0 || proc.crypto != p || proc.intagg != nil || proc.uuidOssp != nil || proc.fuzzy != nil || proc.provider != nil || proc.queryCodec != nil || proc.conversion.Name() != "" || proc.dictionary != (ids.ObjectAddress{}) || proc.result != member.Result || proc.volatility != member.Volatility || proc.strict != member.Strict || seen[member] || v.state.names[descriptorNameKey(proc)] != address {
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
	if v.state.descriptors[proc.address] != proc || proc.crypto != p {
		return fmt.Errorf("stuntgres: invalid pgcrypto procedure identity")
	}
	ext, ok := v.state.descriptors[proc.extension].(*extensionDescriptor)
	if !ok {
		return fmt.Errorf("stuntgres: missing pgcrypto installation")
	}
	return p.validate(v, ext)
}
