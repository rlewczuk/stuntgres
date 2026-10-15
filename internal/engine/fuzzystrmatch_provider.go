package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
)

// Only the catalog-selected overloads are installed, not adjacent fuzzy functions.
type fuzzystrmatchProvider struct{ version string }

var stockFuzzystrmatch = &fuzzystrmatchProvider{version: "1.2"}

var fuzzystrmatchMembers = [...]struct {
	name      string
	signature procedureSignature
	result    OID
}{
	{"daitch_mokotoff", "25", 1009},
	{"levenshtein", "25,25", 23},
	{"levenshtein", "25,25,23,23,23", 23},
	{"levenshtein_less_equal", "25,25,23", 23},
	{"levenshtein_less_equal", "25,25,23,23,23,23", 23},
	{"metaphone", "25,23", 25},
	{"dmetaphone", "25", 25},
	{"dmetaphone_alt", "25", 25},
	{"soundex", "25", 25},
	{"difference", "25,25", 23},
}

func fuzzystrmatchResult(name string, signature procedureSignature) OID {
	for _, member := range fuzzystrmatchMembers {
		if member.name == name && member.signature == signature {
			return member.result
		}
	}
	return 0
}

func (p *fuzzystrmatchProvider) stage(e *Engine, stage *CatalogStage, ext *extensionDescriptor, owner authorization.PrincipalRef, auth AuthorizationView) error {
	ext.members = make([]ObjectAddress, len(fuzzystrmatchMembers))
	for i, member := range fuzzystrmatchMembers {
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
		proc := &procedureDescriptor{address: address, namespace: ext.targetNamespace, extension: ext.address, revision: revision, name: member.name, signature: member.signature, owner: owner, result: member.result, volatility: Immutable, strict: true, fuzzy: p}
		if stage.lookupName(descriptorNameKey(proc)) != (ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.name)}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return nil
}

func (p *fuzzystrmatchProvider) validate(v CatalogRead, ext *extensionDescriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid fuzzystrmatch manifest") }
	if p != stockFuzzystrmatch || ext.name != "fuzzystrmatch" || ext.version != p.version || len(ext.members) != len(fuzzystrmatchMembers) {
		return invalid()
	}
	var seen [len(fuzzystrmatchMembers)]bool
	var owner authorization.PrincipalRef
	for i, address := range ext.members {
		proc, ok := v.state.descriptors[address].(*procedureDescriptor)
		if !ok || address.ClassID != ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.address != address || proc.namespace != ext.targetNamespace || proc.extension != ext.address || proc.revision == 0 || proc.volatility != Immutable || !proc.strict || proc.fuzzy != p || proc.crypto != nil || proc.provider != nil || proc.queryCodec != nil || proc.conversion.Name() != "" || proc.dictionary != (ObjectAddress{}) || v.state.names[descriptorNameKey(proc)] != address {
			return invalid()
		}
		found := false
		for j, member := range fuzzystrmatchMembers {
			if proc.name == member.name && proc.signature == member.signature && proc.result == member.result {
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
		if i == 0 {
			owner = proc.owner
		}
		if owner == (authorization.PrincipalRef{}) || proc.owner != owner {
			return invalid()
		}
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
	if v.state.descriptors[proc.address] != proc || proc.fuzzy != p {
		return fmt.Errorf("stuntgres: foreign fuzzystrmatch procedure")
	}
	for _, address := range ext.members {
		if address == proc.address {
			return nil
		}
	}
	return fmt.Errorf("stuntgres: nonmember fuzzystrmatch procedure")
}
