package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
)

type pgcryptoProvider struct{ version string }

var stockPgcrypto = &pgcryptoProvider{version: "1.4"}

type pgcryptoMember struct {
	name       string
	signature  procedureSignature
	result     OID
	volatility Volatility
	strict     bool
}

var pgcryptoMembers = [...]pgcryptoMember{
	{"digest", "25,25", 17, Immutable, true},
	{"digest", "17,25", 17, Immutable, true},
	{"hmac", "25,25,25", 17, Immutable, true},
	{"hmac", "17,17,25", 17, Immutable, true},
	{"crypt", "25,25", 25, Immutable, true},
	{"gen_salt", "25", 25, Volatile, true},
	{"gen_salt", "25,23", 25, Volatile, true},
	{"encrypt", "17,17,25", 17, Immutable, true},
	{"decrypt", "17,17,25", 17, Immutable, true},
	{"encrypt_iv", "17,17,17,25", 17, Immutable, true},
	{"decrypt_iv", "17,17,17,25", 17, Immutable, true},
	{"gen_random_bytes", "23", 17, Volatile, true},
	{"gen_random_uuid", "", 2950, Volatile, false},
	{"fips_mode", "", 16, Volatile, true},
}

func pgcryptoLookup(name string, signature procedureSignature) (pgcryptoMember, bool) {
	for _, member := range pgcryptoMembers {
		if member.name == name && member.signature == signature {
			return member, true
		}
	}
	return pgcryptoMember{}, false
}

func (p *pgcryptoProvider) stage(e *Engine, stage *CatalogStage, ext *extensionDescriptor, owner authorization.PrincipalRef, auth AuthorizationView) error {
	ext.members = make([]ObjectAddress, len(pgcryptoMembers))
	for i, member := range pgcryptoMembers {
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
		proc := &procedureDescriptor{address: address, namespace: ext.targetNamespace, extension: ext.address, revision: revision, name: member.name, signature: member.signature, owner: owner, result: member.result, volatility: member.volatility, strict: member.strict, crypto: p}
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
	if p != stockPgcrypto || ext.name != "pgcrypto" || ext.version != p.version || len(ext.members) != len(pgcryptoMembers) {
		return invalid()
	}
	seen := make(map[pgcryptoMember]bool, len(pgcryptoMembers))
	var owner authorization.PrincipalRef
	for i, address := range ext.members {
		proc, ok := v.state.descriptors[address].(*procedureDescriptor)
		if !ok {
			return invalid()
		}
		member, admitted := pgcryptoLookup(proc.name, proc.signature)
		if !admitted || address.ClassID != ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.address != address || proc.namespace != ext.targetNamespace || proc.extension != ext.address || proc.revision == 0 || proc.crypto != p || proc.intagg != nil || proc.uuidOssp != nil || proc.fuzzy != nil || proc.provider != nil || proc.queryCodec != nil || proc.conversion.Name() != "" || proc.dictionary != (ObjectAddress{}) || proc.result != member.result || proc.volatility != member.volatility || proc.strict != member.strict || seen[member] || v.state.names[descriptorNameKey(proc)] != address {
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
