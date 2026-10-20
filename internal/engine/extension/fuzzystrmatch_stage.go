package extension

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// Stage installs the fuzzystrmatch 1.1 functions into stage, owned by owner,
// allocating identities in member order.
func (p *FuzzystrmatchProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	if ext.Version != "1.1" {
		return fmt.Errorf("stuntgres: invalid fuzzystrmatch base")
	}
	ext.Members = make([]ids.ObjectAddress, 0, len(FuzzystrmatchMembers))
	return p.stageMembers(stage, ext, owner, 1, len(FuzzystrmatchMembers))
}

// Updates lists the executable 1.1 to 1.2 transition.
func (p *FuzzystrmatchProvider) Updates() []UpdateAction {
	return []UpdateAction{{Edge: catalog.ExtensionVersionEdge{From: "1.1", To: "1.2"}, Stage: p.stageUpdate}}
}

func (p *FuzzystrmatchProvider) stageUpdate(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	if ext.Version != "1.1" {
		return fmt.Errorf("stuntgres: invalid fuzzystrmatch update predecessor")
	}
	if err := p.stageMembers(stage, ext, owner, 0, 1); err != nil {
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

func (p *FuzzystrmatchProvider) stageMembers(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, start, end int) error {
	for _, member := range FuzzystrmatchMembers[start:end] {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		ext.Members = append(ext.Members, address)
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.Name, Signature: member.Signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: member.Result, Volatility: expr.Immutable, Strict: true, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return nil
}
