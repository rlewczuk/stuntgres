package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) resolveAlterDomainLocked(auth AuthorizationView, name parser.ObjectName) (*catalog.Type, ids.ObjectRef, error) {
	lookup := name
	if len(name.Parts) == 2 {
		parts := [2]parser.Identifier{name.Parts[0], name.Parts[1]}
		parts[0].Pos = parser.Position{}
		lookup.Parts = parts[:]
	}
	path, err := e.lookupNamespaces(auth, lookup, typeLookup)
	if err != nil {
		return nil, ids.ObjectRef{}, err
	}
	for i := range path.count() {
		found := auth.Catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.at(i), Name: name.Parts[len(name.Parts)-1].Text})
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
	return nil, ids.ObjectRef{}, sqlError("42704", fmt.Sprintf("type %q does not exist", objectNameText(name)))
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
	stage.authorizeLocked(auth, op.session)
	scope.routineAuth = auth
	scope.domains.inputCatalog = auth.Catalog
	stage.domainBases = append(stage.domainBases, old)
	next := *old
	next.DomainChecks = slices.Clone(old.DomainChecks)
	selected := -1
	for i, check := range next.DomainChecks {
		if check.Name == raw.Constraint.Text {
			selected = i
			break
		}
	}
	validating := raw.Action == parser.DomainValidateCheck || raw.Action == parser.DomainAddCheck && !raw.Check.NotValid
	switch raw.Action {
	case parser.DomainAddCheck:
		check := *raw.Check
		// REL_18_0 ALTER accepts but ignores NO INHERIT. CREATE keeps its
		// own cooking rejection; never relax the shared CREATE binder.
		check.NoInherit = false
		err = e.bindDomainChecks(op, auth, stage, &next, []parser.ColumnConstraint{{Check: &check, Pos: check.Pos}}, scope.input)
		if err == nil {
			for i, c := range next.DomainChecks {
				if !slices.ContainsFunc(old.DomainChecks, func(prior catalog.DomainCheckConstraint) bool { return prior.OID == c.OID }) {
					selected = i
					break
				}
			}
			next.SQLType, err = sqltype.Domain(old.SQLType.ID(), old.Name(), old.SQLType.SQLName(), old.SQLType.DomainBase(), true, old.SQLType.OwnDomainNotNull())
		}
	case parser.DomainDropCheck, parser.DomainValidateCheck:
		if selected < 0 {
			message := fmt.Sprintf("constraint %q of domain %q does not exist", raw.Constraint.Text, objectNameText(raw.Name))
			if raw.Action == parser.DomainDropCheck && raw.IfExists {
				schemaNotice(op, "00000", message+", skipping")
				e.mu.Unlock()
				e.DiscardCatalogStage(stage)
				return Result{}, nil
			}
			err = sqlError("42704", message)
		} else if raw.Action == parser.DomainDropCheck {
			err = stage.removeDomainCheck(old, next.DomainChecks[selected].OID)
		} else {
			next.DomainChecks[selected].NotValid = false
		}
	default:
		err = fmt.Errorf("stuntgres: invalid domain constraint action")
	}
	if err == nil && raw.Action != parser.DomainDropCheck {
		next.ObjectRevision, err = e.catalog.revisionLocked()
		if err == nil {
			err = stage.replaceDescriptor(&next, false)
		}
	}
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
		evaluation := EvalContext{evalStatement: evalStatement{Context: op.Context(), host: hostOf(scope), catalog: auth.Catalog, Input: scope.input, Output: scope.output}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
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
			if err := evaluation.validateSelectedDomainRows(auth.Catalog, &chosen, use, frontier, writes, deletes); err != nil {
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

func (c *EvalContext) evaluateSelectedDomainCheck(view catalog.Read, selected *catalog.Type, value sqltype.Value, violation error) error {
	oldValue, oldUsage, oldActive, oldChecks := c.domainValue, c.domainFrameUsage, c.domainValueActive, c.Input.DomainChecks
	c.Input.DomainChecks = c
	defer func() {
		c.domainValue, c.domainFrameUsage, c.domainValueActive, c.Input.DomainChecks = oldValue, oldUsage, oldActive, oldChecks
	}()
	return c.evaluateOwnDomainChecks(view, selected, selected, value, violation)
}

func (c *EvalContext) validateSelectedDomainRows(view catalog.Read, selected *catalog.Type, use typedef.DomainValidationUse, frontier ids.CommitSeq, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) error {
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
				if err := c.evaluateSelectedDomainCheck(view, selected, row.Values[column], violations[i]); err != nil {
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
