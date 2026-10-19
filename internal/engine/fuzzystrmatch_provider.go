package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// Only the catalog-selected overloads are installed, not adjacent fuzzy functions.
type fuzzystrmatchProvider struct{ control catalog.ExtensionControl }

// Control implements extensionImplementation.
func (p *fuzzystrmatchProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *fuzzystrmatchProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.1"}, Edges: []catalog.ExtensionVersionEdge{{From: "1.0", To: "1.1"}, {From: "1.1", To: "1.2"}}}
}

var stockFuzzystrmatch = &fuzzystrmatchProvider{control: catalog.ExtensionControl{Version: "1.2", Trusted: true, Superuser: true, Relocatable: true, Comment: new("determine similarities and distance between strings")}}

var fuzzystrmatchMembers = [...]struct {
	name      string
	signature catalog.ProcedureSignature
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

func fuzzystrmatchResult(name string, signature catalog.ProcedureSignature) ids.OID {
	for _, member := range fuzzystrmatchMembers {
		if member.name == name && member.signature == signature {
			return member.result
		}
	}
	return 0
}

func (p *fuzzystrmatchProvider) stage(e *Engine, stage *CatalogStage, ext *catalog.Extension, owner authorization.PrincipalRef, auth AuthorizationView) error {
	if ext.Version != "1.1" {
		return fmt.Errorf("stuntgres: invalid fuzzystrmatch base")
	}
	ext.Members = make([]ids.ObjectAddress, 0, len(fuzzystrmatchMembers))
	return p.stageMembers(e, stage, ext, owner, 1, len(fuzzystrmatchMembers))
}

func (p *fuzzystrmatchProvider) updates() []extensionUpdateAction {
	return []extensionUpdateAction{{edge: catalog.ExtensionVersionEdge{From: "1.1", To: "1.2"}, stage: p.stageUpdate}}
}

func (p *fuzzystrmatchProvider) stageUpdate(e *Engine, stage *CatalogStage, ext *catalog.Extension, owner authorization.PrincipalRef, auth AuthorizationView) error {
	if ext.Version != "1.1" {
		return fmt.Errorf("stuntgres: invalid fuzzystrmatch update predecessor")
	}
	if err := p.stageMembers(e, stage, ext, owner, 0, 1); err != nil {
		return err
	}
	// Retain the established selected 1.2 manifest order without changing
	// any of the nine old member identities or descriptors.
	dm := ext.Members[len(ext.Members)-1]
	copy(ext.Members[1:], ext.Members[:len(ext.Members)-1])
	ext.Members[0] = dm
	ext.Version = "1.2"
	return nil
}

func (p *fuzzystrmatchProvider) stageMembers(e *Engine, stage *CatalogStage, ext *catalog.Extension, owner authorization.PrincipalRef, start, end int) error {
	for _, member := range fuzzystrmatchMembers[start:end] {
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		revision, err := e.catalog.revisionLocked()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		ext.Members = append(ext.Members, address)
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.name, Signature: member.signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: member.result, Volatility: expr.Immutable, Strict: true, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return nil
}

func (p *fuzzystrmatchProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid fuzzystrmatch manifest") }
	count := len(fuzzystrmatchMembers)
	if ext.Version == "1.1" {
		count--
	}
	if v.Owner() == nil || p != stockFuzzystrmatch || ext.Name() != "fuzzystrmatch" || (ext.Version != "1.1" && ext.Version != "1.2") || len(ext.Members) != count {
		return invalid()
	}
	var seen [len(fuzzystrmatchMembers)]bool
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.Address() != address || proc.Namespace() != ext.TargetNamespace || proc.Extension != ext.Address() || proc.Revision() == 0 || proc.RoutineKind != catalog.FunctionRoutine || proc.Volatility != expr.Immutable || !proc.Strict || proc.Implementation != p || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) || v.NameAddress(catalog.DescriptorNameKey(proc)) != address {
			return invalid()
		}
		found := false
		for j, member := range fuzzystrmatchMembers {
			if ext.Version == "1.1" && j == 0 {
				continue
			}
			if proc.Name() == member.name && proc.Signature == member.signature && proc.Result == member.result {
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
		if proc.Owner.ID() == 0 || proc.Owner != v.Owner().Principals.Principal(proc.Owner.ID()) {
			return invalid()
		}
	}
	return nil
}

func (p *fuzzystrmatchProvider) validateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing fuzzystrmatch installation")
	}
	if err := p.ValidateExtension(v, ext); err != nil {
		return err
	}
	if v.Get(proc.Address()) != proc || fuzzystrmatchProviderOf(proc) != p {
		return fmt.Errorf("stuntgres: foreign fuzzystrmatch procedure")
	}
	for _, address := range ext.Members {
		if address == proc.Address() {
			return nil
		}
	}
	return fmt.Errorf("stuntgres: nonmember fuzzystrmatch procedure")
}
