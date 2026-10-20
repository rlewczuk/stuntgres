package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) resolveAlterDomainLocked(auth AuthorizationView, name parser.ObjectName) (*catalog.Type, ids.ObjectRef, error) {
	lookup := name
	if len(name.Parts) == 2 {
		parts := [2]parser.Identifier{name.Parts[0], name.Parts[1]}
		parts[0].Pos = parser.Position{}
		lookup.Parts = parts[:]
	}
	path, err := e.lookupNamespaces(auth, lookup, analyze.TypeLookup)
	if err != nil {
		return nil, ids.ObjectRef{}, err
	}
	for i := range path.Count() {
		found := auth.Catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.At(i), Name: name.Parts[len(name.Parts)-1].Text})
		if len(found) == 0 {
			continue
		}
		d := auth.Catalog.MustType(found[0])
		ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())
		diagnosticPath, err := e.referencePath(auth)
		if err != nil {
			return nil, ref, err
		}
		if !d.SQLType.IsDomain() {
			return nil, ref, sqlError("42809", d.SQLType.DiagnosticName()+" is not a domain")
		}
		if err := auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
				err = sqlError("42501", "must be owner of type "+catalog.TypeDiagnosticNameAt(auth.Catalog, d, diagnosticPath))
			}
			return nil, ref, err
		}
		return d, ref, nil
	}
	return nil, ids.ObjectRef{}, sqlError("42704", fmt.Sprintf("type %q does not exist", catalog.ObjectNameText(name)))
}

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
		original, ref, err = e.resolveAlterDomainLocked(auth, raw.Name)
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
		old, current, err = e.resolveAlterDomainLocked(auth, raw.Name)
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
	edit, err := typedef.StageAlterDomainConstraintLocked(typedefHost{e: e, op: op, stage: stage}, auth, old, raw, scope.input)
	if err == nil && edit.Skipped {
		e.mu.Unlock()
		e.DiscardCatalogStage(stage)
		return Result{}, nil
	}
	next, selected, validating := edit.Next, edit.Selected, edit.Validating
	var diagnosticPath []ids.ObjectAddress
	if err == nil && validating {
		diagnosticPath, err = e.referencePath(auth)
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return Result{}, err
	}
	if validating {
		chosen := next
		chosen.DomainChecks = []catalog.DomainCheckConstraint{next.DomainChecks[selected]}
		scope.routineDependencies = chosen.DomainChecks[0].Dependencies
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: hostOf(scope), Catalog: auth.Catalog, Input: scope.input, Output: scope.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		evaluation.Input.TypeDiagnosticName, err = e.captureTypeDiagnosticName(auth)
		if err != nil {
			return Result{}, err
		}
		if err := prepareOwnDomainChecks(&evaluation, &chosen); err != nil {
			return Result{}, err
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
			if err := validateSelectedDomainRows(&evaluation, auth.Catalog, &chosen, use, frontier, writes, deletes); err != nil {
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

func validateSelectedDomainRows(c *exec.Context, view catalog.Read, selected *catalog.Type, use typedef.DomainValidationUse, frontier ids.CommitSeq, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) error {
	violations := make([]error, len(use.Columns))
	for i, column := range use.Columns {
		violations[i] = typedef.DomainHistoricalViolation(view, use.Relation, column)
	}
	var replaced map[ids.RowID]bool
	for _, row := range writes {
		if row.Base != 0 {
			if replaced == nil {
				replaced = make(map[ids.RowID]bool)
			}
			replaced[row.Row] = true
		}
	}
	scan := func(rows []storage.RowVersion, committed bool) error {
		for _, row := range rows {
			if err := c.Context.Err(); err != nil {
				return err
			}
			if committed && replaced[row.Row] || !visibility.LiveVersion(row, frontier, use.Ends, deletes) {
				continue
			}
			for i, column := range use.Columns {
				if err := c.EvaluateSelectedDomainCheck(view, selected, row.Values[column], violations[i]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := scan(use.Versions, true); err != nil {
		return err
	}
	return scan(writes, false)
}
