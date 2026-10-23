package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

func (e *Engine) executeAlterDomain(scope *StatementScope, raw *parser.AlterDomain, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.op
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	var original *catalog.Type
	var ref ids.ObjectRef
	if err == nil {
		original, ref, err = typedef.ResolveAlterDomainLocked(ddlHost{e: e, op: op}, auth, raw.Name)
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = scope.acquireSequence(ref, lock.TypeExclusive, true); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	auth, err = e.authorizationLocked(op)
	var old *catalog.Type
	var current ids.ObjectRef
	if err == nil {
		old, current, err = typedef.ResolveAlterDomainLocked(ddlHost{e: e, op: op}, auth, raw.Name)
	}
	if err == nil && (ref != current || old != original) {
		err = ids.ErrCatalogReplaced
	}
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.domainAlterState().Ctx = op.Context()
	stage.authorizeLocked(auth, op.Session)
	scope.routineAuth = auth
	scope.domains.inputCatalog = auth.Catalog
	stage.domainBases = append(stage.domainBases, old)
	edit, err := typedef.StageAlterDomainLocked(ddlHost{e: e, op: op, stage: stage}, auth, old, raw, scope.input, scope.output)
	if err == nil && edit.Skipped {
		e.mu.Unlock()
		e.DiscardCatalogStage(stage)
		return Result{}, nil
	}
	next, selected, validation := edit.Next, edit.Selected, edit.Validation
	validating := validation != typedef.DomainValidateNone
	var diagnosticPath []ids.ObjectAddress
	if err == nil && validating {
		diagnosticPath, err = referencePath(auth)
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return Result{}, err
	}
	if validating {
		chosen := next
		if validation == typedef.DomainValidateValues {
			chosen.DomainChecks = []catalog.DomainCheckConstraint{next.DomainChecks[selected]}
			scope.routineDependencies = chosen.DomainChecks[0].Dependencies
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: hostOf(scope), Catalog: auth.Catalog, Input: scope.input, Output: scope.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		if validation == typedef.DomainValidateValues {
			evaluation.Input.TypeDiagnosticName, err = captureTypeDiagnosticName(auth)
			if err != nil {
				return Result{}, err
			}
			if err := prepareOwnDomainChecks(&evaluation, &chosen); err != nil {
				return Result{}, err
			}
		}
		fence, discoveryErr := typedef.DomainUsesAt(auth.Catalog, ref.Address, diagnosticPath)
		if fence != nil {
			for _, relation := range fence.Reads {
				if err := scope.acquireSequence(e.catalog.Domain.Ref(ref.Epoch, relation.Address()), lock.AccessShare, true); err != nil {
					return Result{}, err
				}
			}
		}
		if discoveryErr != nil {
			if fence != nil {
				e.mu.Lock()
				current := e.stageCatalogBaseLocked(stage)
				stable := current.Epoch() == ref.Epoch
				for address, descriptor := range fence.Objects {
					stable = stable && current.Get(address) == descriptor
				}
				e.mu.Unlock()
				if !stable {
					return Result{}, catalog.Conflict{}
				}
			}
			return Result{}, discoveryErr
		}
		for _, use := range fence.Uses {
			if err := scope.acquireSequence(e.catalog.Domain.Ref(ref.Epoch, use.Relation.Address()), lock.Share, true); err != nil {
				return Result{}, err
			}
		}
		e.mu.Lock()
		view := e.catalog.read(e.stageCatalogBaseLocked(stage))
		currentUses, graphErr := typedef.DomainUsesAt(view, ref.Address, diagnosticPath)
		if graphErr == nil && !typedef.DomainGraphEqual(fence, currentUses) {
			graphErr = catalog.Conflict{}
		}
		frontier := e.visibility.CommitSeq()
		if graphErr == nil {
			for i := range fence.Uses {
				use := &fence.Uses[i]
				use.Storage = e.stageStorageLocked(stage, use.Relation.Address())
				if use.Storage == nil {
					graphErr = catalog.Conflict{}
					break
				}
				use.Versions = use.Storage.Versions[:len(use.Storage.Versions):len(use.Storage.Versions)]
				use.Ends, use.Generation = use.Storage.Ends, use.Storage.Generation
			}
		}
		e.mu.Unlock()
		if graphErr != nil {
			return Result{}, graphErr
		}
		stage.domainAlterState().Validation = fence
		for _, use := range fence.Uses {
			writes, deletes := scope.columnProjectionRoots(use.Relation.Address())
			if err := table.ValidateSelectedDomainRows(&evaluation, auth.Catalog, &chosen, use, frontier, writes, deletes, validation); err != nil {
				return Result{}, err
			}
		}
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: ref.Epoch})
	return Result{}, err
}
