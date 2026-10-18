package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// Only the catalog-selected overloads are installed, not adjacent fuzzy functions.
type fuzzystrmatchProvider struct{ version string }

var stockFuzzystrmatch = &fuzzystrmatchProvider{version: "1.2"}

var fuzzystrmatchMembers = [...]struct {
	name      string
	signature procedureSignature
	result    ids.OID
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

func fuzzystrmatchResult(name string, signature procedureSignature) ids.OID {
	for _, member := range fuzzystrmatchMembers {
		if member.name == name && member.signature == signature {
			return member.result
		}
	}
	return 0
}

func (p *fuzzystrmatchProvider) stage(e *Engine, stage *CatalogStage, ext *extensionDescriptor, owner authorization.PrincipalRef, auth AuthorizationView) error {
	ext.members = make([]ids.ObjectAddress, len(fuzzystrmatchMembers))
	for i, member := range fuzzystrmatchMembers {
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
		proc := &procedureDescriptor{address: address, namespace: ext.targetNamespace, extension: ext.address, revision: revision, name: member.name, signature: member.signature, routineKind: functionRoutine, owner: owner, result: member.result, volatility: Immutable, strict: true, fuzzy: p}
		if stage.lookupName(descriptorNameKey(proc)) != (ids.ObjectAddress{}) {
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
	if v.owner == nil || p != stockFuzzystrmatch || ext.name != "fuzzystrmatch" || ext.version != p.version || len(ext.members) != len(fuzzystrmatchMembers) {
		return invalid()
	}
	var seen [len(fuzzystrmatchMembers)]bool
	for _, address := range ext.members {
		proc, ok := v.state.descriptors[address].(*procedureDescriptor)
		if !ok || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.address != address || proc.namespace != ext.targetNamespace || proc.extension != ext.address || proc.revision == 0 || proc.routineKind != functionRoutine || proc.volatility != Immutable || !proc.strict || proc.fuzzy != p || proc.intagg != nil || proc.uuidOssp != nil || proc.crypto != nil || proc.provider != nil || proc.queryCodec != nil || proc.conversion.Name() != "" || proc.dictionary != (ids.ObjectAddress{}) || v.state.names[descriptorNameKey(proc)] != address {
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
		// Membership fixes the exact provider, not separately transferable
		// function ownership. Every member retains real typed provenance.
		if proc.owner.ID() == 0 || proc.owner != v.owner.principals.Principal(proc.owner.ID()) {
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
