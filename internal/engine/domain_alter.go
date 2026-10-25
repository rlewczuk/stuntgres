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

func executeAlterDomain(e *Engine, scope *StatementScope, raw *parser.AlterDomain, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.Op
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var original *catalog.Type
	var ref ids.ObjectRef
	if err == nil {
		original, ref, err = typedef.ResolveAlterDomainLocked(DDLHost{E: e, Op: op}, auth, raw.Name)
	}
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = scope.AcquireSequence(ref, lock.TypeExclusive, true); err != nil {
		return Result{}, err
	}
	e.Mu.Lock()
	auth, err = e.AuthorizationLocked(op)
	var old *catalog.Type
	var current ids.ObjectRef
	if err == nil {
		old, current, err = typedef.ResolveAlterDomainLocked(DDLHost{E: e, Op: op}, auth, raw.Name)
	}
	if err == nil && (ref != current || old != original) {
		err = ids.ErrCatalogReplaced
	}
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.DomainAlterState().Ctx = op.Context()
	stage.AuthorizeLocked(auth, op.Session)
	scope.RoutineAuth = auth
	scope.Domains.InputCatalog = auth.Catalog
	stage.DomainBases = append(stage.DomainBases, old)
	edit, err := typedef.StageAlterDomainLocked(TypeHost{TableHost{DDLHost{E: e, Op: op, CatalogStage: stage}}}, auth, old, raw, scope.Input, scope.Output)
	if err == nil && edit.Skipped {
		e.Mu.Unlock()
		e.DiscardCatalogStage(stage)
		return Result{}, nil
	}
	next, selected, validation := edit.Next, edit.Selected, edit.Validation
	validating := validation != typedef.DomainValidateNone
	var diagnosticPath []ids.ObjectAddress
	if err == nil && validating {
		diagnosticPath, err = ReferencePath(auth)
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return Result{}, err
	}
	if validating {
		chosen := next
		if validation == typedef.DomainValidateValues {
			chosen.DomainChecks = []catalog.DomainCheckConstraint{next.DomainChecks[selected]}
			scope.RoutineDependencies = chosen.DomainChecks[0].Dependencies
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		if validation == typedef.DomainValidateValues {
			evaluation.Input.TypeDiagnosticName, err = CaptureTypeDiagnosticName(auth)
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
				if err := scope.AcquireSequence(e.Catalog.Domain.Ref(ref.Epoch, relation.Address()), lock.AccessShare, true); err != nil {
					return Result{}, err
				}
			}
		}
		if discoveryErr != nil {
			if fence != nil {
				e.Mu.Lock()
				current := e.StageCatalogBaseLocked(stage)
				stable := current.Epoch() == ref.Epoch
				for address, descriptor := range fence.Objects {
					stable = stable && current.Get(address) == descriptor
				}
				e.Mu.Unlock()
				if !stable {
					return Result{}, catalog.Conflict{}
				}
			}
			return Result{}, discoveryErr
		}
		for _, use := range fence.Uses {
			if err := scope.AcquireSequence(e.Catalog.Domain.Ref(ref.Epoch, use.Relation.Address()), lock.Share, true); err != nil {
				return Result{}, err
			}
		}
		e.Mu.Lock()
		view := e.Catalog.Read(e.StageCatalogBaseLocked(stage))
		currentUses, graphErr := typedef.DomainUsesAt(view, ref.Address, diagnosticPath)
		if graphErr == nil && !typedef.DomainGraphEqual(fence, currentUses) {
			graphErr = catalog.Conflict{}
		}
		frontier := e.Visibility.CommitSeq()
		if graphErr == nil {
			for i := range fence.Uses {
				use := &fence.Uses[i]
				use.Storage = e.StageStorageLocked(stage, use.Relation.Address())
				if use.Storage == nil {
					graphErr = catalog.Conflict{}
					break
				}
				use.Versions = use.Storage.Versions[:len(use.Storage.Versions):len(use.Storage.Versions)]
				use.Ends, use.Generation = use.Storage.Ends, use.Storage.Generation
			}
		}
		e.Mu.Unlock()
		if graphErr != nil {
			return Result{}, graphErr
		}
		stage.DomainAlterState().Validation = fence
		for _, use := range fence.Uses {
			writes, deletes := scope.ColumnProjectionRoots(use.Relation.Address())
			if err := table.ValidateSelectedDomainRows(&evaluation, auth.Catalog, &chosen, use, frontier, writes, deletes, validation); err != nil {
				return Result{}, err
			}
		}
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.PublishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: ref.Epoch})
	return Result{}, err
}
