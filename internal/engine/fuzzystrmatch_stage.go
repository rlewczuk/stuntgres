package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// fuzzystrmatchInstaller stages the fuzzystrmatch provider for the engine.
// Installed members hold the bare provider, never the installer.
type fuzzystrmatchInstaller struct {
	*extension.FuzzystrmatchProvider
}

func (p fuzzystrmatchInstaller) stage(e *Engine, stage *CatalogStage, ext *catalog.Extension, owner authorization.PrincipalRef, auth AuthorizationView) error {
	if ext.Version != "1.1" {
		return fmt.Errorf("stuntgres: invalid fuzzystrmatch base")
	}
	ext.Members = make([]ids.ObjectAddress, 0, len(extension.FuzzystrmatchMembers))
	return p.stageMembers(e, stage, ext, owner, 1, len(extension.FuzzystrmatchMembers))
}

func (p fuzzystrmatchInstaller) updates() []extensionUpdateAction {
	return []extensionUpdateAction{{edge: catalog.ExtensionVersionEdge{From: "1.1", To: "1.2"}, stage: p.stageUpdate}}
}

func (p fuzzystrmatchInstaller) stageUpdate(e *Engine, stage *CatalogStage, ext *catalog.Extension, owner authorization.PrincipalRef, auth AuthorizationView) error {
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

func (p fuzzystrmatchInstaller) stageMembers(e *Engine, stage *CatalogStage, ext *catalog.Extension, owner authorization.PrincipalRef, start, end int) error {
	for _, member := range extension.FuzzystrmatchMembers[start:end] {
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
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.Name, Signature: member.Signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: member.Result, Volatility: expr.Immutable, Strict: true, Implementation: p.FuzzystrmatchProvider}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return nil
}
