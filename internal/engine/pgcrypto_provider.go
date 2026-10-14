package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
)

type pgcryptoProvider struct{ version string }

var stockPgcrypto = &pgcryptoProvider{version: "1.4"}

var pgcryptoSignatures = [...]procedureSignature{"25,25", "17,25", "25,25,25", "17,17,25"}

func pgcryptoName(signature procedureSignature) string {
	switch signature {
	case "25,25", "17,25":
		return "digest"
	case "25,25,25", "17,17,25":
		return "hmac"
	}
	return ""
}

func (p *pgcryptoProvider) stage(e *Engine, stage *CatalogStage, ext *extensionDescriptor, owner authorization.PrincipalRef, auth AuthorizationView) error {
	ext.members = make([]ObjectAddress, len(pgcryptoSignatures))
	for i, signature := range pgcryptoSignatures {
		oid, err := e.allocateOIDLocked()
		if err != nil {
			return err
		}
		revision, err := e.revisionLocked()
		if err != nil {
			return err
		}
		address := ObjectAddress{ClassID: ProcedureClassID, ObjectID: oid}
		ext.members[i] = address
		proc := &procedureDescriptor{address: address, namespace: ext.targetNamespace, extension: ext.address, revision: revision, name: pgcryptoName(signature), signature: signature, owner: owner, result: 17, volatility: Immutable, strict: true, crypto: p}
		if stage.lookupName(descriptorNameKey(proc)) != (ObjectAddress{}) {
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
	if p != stockPgcrypto || ext.name != "pgcrypto" || ext.version != p.version || len(ext.members) != len(pgcryptoSignatures) {
		return invalid()
	}
	seen := make(map[procedureSignature]bool, len(pgcryptoSignatures))
	var owner authorization.PrincipalRef
	for i, address := range ext.members {
		proc, ok := v.state.descriptors[address].(*procedureDescriptor)
		if !ok || address.ClassID != ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.address != address || proc.namespace != ext.targetNamespace || proc.extension != ext.address || proc.revision == 0 || proc.crypto != p || proc.fuzzy != nil || proc.provider != nil || proc.queryCodec != nil || proc.conversion.Name() != "" || proc.dictionary != (ObjectAddress{}) || proc.name != pgcryptoName(proc.signature) || proc.name == "" || proc.result != 17 || proc.volatility != Immutable || !proc.strict || seen[proc.signature] || v.state.names[descriptorNameKey(proc)] != address {
			return invalid()
		}
		if i == 0 {
			owner = proc.owner
		} else if owner != proc.owner {
			return invalid()
		}
		if owner == (authorization.PrincipalRef{}) {
			return invalid()
		}
		seen[proc.signature] = true
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
