package engine

import (
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/sqlstate"
)

type regtypeAnalysis struct {
	constants         []regtypeConstant
	path              sequence.Environment
	role              authorization.PrincipalRef
	database          string
	types, collations bool
	collationPath     []ids.ObjectAddress
	invalidated       bool // A failed reanalysis must not revive these captures.
}

func (e *Engine) regtypeAnalysisLocked(auth AuthorizationView, constants []regtypeConstant) (*regtypeAnalysis, error) {
	if len(constants) == 0 {
		return nil, nil
	}
	cell := &regtypeAnalysis{constants: slices.Clone(constants), role: auth.State.CurrentUser, database: auth.session.databaseName}
	for _, constant := range constants {
		if constant.Operator != 0 || constant.Value.Type() == nil || constant.Value.Type().ContainsRegtype() || constant.Value.Type().ContainsOperatorAlias() || constant.Value.Type().ContainsRegproc() {
			cell.types = true
		}
		for _, reference := range constant.References {
			cell.collations = cell.collations || reference.Address.ClassID == ids.CollationClassID
		}
	}
	var err error
	if cell.types {
		cell.path, err = e.sequenceEnvironmentLocked(auth)
	}
	if err == nil && cell.collations {
		cell.collationPath, err = e.lookupPath(auth, analyze.CollationLookup)
	}
	return cell, err
}

func (e *Engine) regtypeAnalysisValidLocked(auth AuthorizationView, cell *regtypeAnalysis) (bool, error) {
	if cell != nil && cell.invalidated {
		return false, nil
	}
	if cell == nil {
		return true, nil
	}
	if cell.types {
		path, err := e.sequenceEnvironmentLocked(auth)
		if err != nil {
			return false, err
		}
		if cell.role != auth.State.CurrentUser || cell.database != auth.session.databaseName || !sequence.SameEnvironment(cell.path, path) {
			return false, nil
		}
	}
	if cell.collations {
		path, err := e.lookupPath(auth, analyze.CollationLookup)
		if err != nil {
			return false, err
		}
		if cell.role != auth.State.CurrentUser || cell.database != auth.session.databaseName || !slices.Equal(cell.collationPath, path) {
			return false, nil
		}
	}
	// Preserve qualified namespace authority and operator operand stamps.
	// Collation/namespace aliases additionally reanalyze changed captured names.
	for _, constant := range cell.constants {
		for _, operand := range constant.Operands {
			d := auth.Catalog.Get(operand.Ref.Address)
			if d == nil || operand.Ref.Domain() != e.catalog.Domain || operand.Ref.Epoch != auth.Catalog.Epoch() || d.Revision() != operand.Revision {
				return false, nil
			}
		}
		for _, reference := range constant.References {
			d := auth.Catalog.Get(reference.Address)
			if d == nil || d.Name() != reference.Name || d.Namespace() != reference.Namespace {
				return false, nil
			}
			if reference.Address.ClassID == ids.CollationClassID {
				ns := auth.Catalog.Get(reference.Namespace)
				if ns == nil || ns.Name() != reference.NamespaceName {
					return false, nil
				}
				if err := auth.CanUse(e.catalog.Domain.Ref(auth.Catalog.Epoch(), reference.Namespace)); err != nil {
					if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
						return false, nil
					}
					return false, err
				}
			}
		}
		for _, namespace := range constant.Qualified {
			if auth.Catalog.Get(namespace) == nil {
				return false, nil
			}
			if err := auth.CanUse(e.catalog.Domain.Ref(auth.Catalog.Epoch(), namespace)); err != nil {
				if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
					return false, nil
				}
				return false, err
			}
		}
	}
	return true, nil
}
