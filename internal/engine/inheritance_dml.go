package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Descendant targets belong to the active command, not its SELECT sources or
// another CTE. The named root retains the embedded, allocation-free fast path.
func (s *StatementScope) acquireInheritedMutationTargets(statement parser.Statement) error {
	only := true
	switch command := statement.(type) {
	case *parser.Update:
		only = command.Only
	case *parser.Delete:
		only = command.Only
	}
	if only {
		return nil
	}
	e := s.op.session.Engine()
	for {
		e.mu.Lock()
		view := e.privateCatalogLocked(s.owner.catalog)
		root, ok := view.Relation(s.lockedRef.Address)
		if !ok {
			e.mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		if root.RelationKind != catalog.TableRelation {
			e.mu.Unlock()
			return nil
		}
		for _, target := range s.inheritedTargets {
			if _, err := currentQuerySourceLocked(e, view, s.owner.catalog, target); err != nil {
				e.mu.Unlock()
				return err
			}
		}
		var pending []queryLeaf
		err := inheritanceQueryClosure(view, root, s.op.session.token, func(child *catalog.Relation) error {
			if child.Address() == root.Address() {
				return nil
			}
			for _, target := range s.inheritedTargets {
				if target.ref.Address == child.Address() {
					return nil
				}
			}
			ref := s.lockedRef
			ref.Address = child.Address()
			pending = append(pending, queryLeaf{ref: ref, relation: child, storage: e.privateStorageLocked(s.owner.catalog, child.Address()), only: true})
			return nil
		})
		e.mu.Unlock()
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		for _, target := range pending {
			policy := lock.Policy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
			request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: target.ref}, lock.RowExclusive, policy)
			if request != nil {
				s.admissionWaited = true
				if s.replacementRead {
					e.replacementMu.RUnlock()
					s.replacementRead = false
				}
				err = request.Wait()
			}
			if err != nil {
				return err
			}
			s.owner.mu.Lock()
			implicit := s.owner.state == TxImplicit
			s.owner.mu.Unlock()
			if implicit && !s.replacementRead {
				e.replacementMu.RLock()
				s.replacementRead = true
			}
			e.mu.Lock()
			_, err = currentQuerySourceLocked(e, e.privateCatalogLocked(s.owner.catalog), s.owner.catalog, target)
			if err == nil {
				s.inheritedTargets = append(s.inheritedTargets, target)
				e.markTemporaryAccessLocked(s, target.ref)
			}
			e.mu.Unlock()
			if err != nil {
				return err
			}
		}
	}
}

func (s *StatementScope) mutationTargetRefs(ref ids.ObjectRef) []ids.ObjectRef {
	refs := make([]ids.ObjectRef, 1, len(s.inheritedTargets)+1)
	refs[0] = ref
	for _, target := range s.inheritedTargets {
		refs = append(refs, target.ref)
	}
	return refs
}

func (s *StatementScope) mutationStorage(ref ids.ObjectRef) *storage.Relation {
	if ref == s.lockedRef {
		return s.lockedStorage
	}
	for _, target := range s.inheritedTargets {
		if target.ref == ref {
			return target.storage
		}
	}
	return nil
}

// Capture the already admitted manifest, never rediscover a graph after the
// command view has frozen its roots. Each segment retains real physical rows.
func (s *StatementScope) captureMutationSource(ctx context.Context, view *VisibilityView, b binding) (*querySource, error) {
	if s.rlsTargetDenied {
		return captureDeniedQuerySource(view, b.ref)
	}
	if len(s.inheritedTargets) == 0 {
		return captureQuerySource(ctx, view, b.ref)
	}
	pin := view.Retain()
	if pin == nil {
		return nil, errors.New("stuntgres: visibility view is released")
	}
	source := &querySource{ref: b.ref, view: pin, inheritance: make([]inheritanceQuerySegment, 0, len(s.inheritedTargets)+1), inheritanceEnds: make([]int, len(s.inheritedTargets)+1)}
	root := queryLeaf{ref: b.ref, relation: b.relation, storage: b.storage, only: true}
	for i := 0; i <= len(s.inheritedTargets); i++ {
		target := root
		if i != 0 {
			target = s.inheritedTargets[i-1]
		}
		layout, err := inheritanceQueryLayout(b.relation, target.relation)
		if err != nil {
			source.close()
			return nil, err
		}
		prefix, err := captureQuerySource(ctx, view, target.ref)
		if err != nil {
			source.close()
			return nil, err
		}
		segment := inheritanceQuerySegment{leaf: target, prefix: prefix, layout: layout}
		if layout != nil {
			segment.scratch = make([]sqltype.Value, len(layout))
		}
		source.inheritance = append(source.inheritance, segment)
		total := prefix.count()
		if i != 0 {
			total += source.inheritanceEnds[i-1]
		}
		source.inheritanceEnds[i] = total
	}
	return source, nil
}

func mutationPhysicalRow(source *querySource, index int, b binding) queryPhysicalRow {
	version := source.versionAt(source.rawIndex(index))
	return source.physicalRow(index, queryLeaf{ref: b.ref, relation: b.relation, storage: b.storage}, version)
}

func (row queryPhysicalRow) deletion() deleteCandidate {
	return deleteCandidate{Ref: row.leaf.ref, Storage: row.leaf.storage, Row: row.version.Row, Version: row.version.Version, Base: row.version.Base, Ordinal: row.hint, Own: row.version.Born == 0}
}

type mutationRowIdentity struct {
	ref ids.ObjectRef
	row ids.RowID
}

// Physical generation/checks use the full physical proposal; diagnostic values
// and SELECT rights are projected back to the named mutation target.
func (e *Engine) validateInheritedMutationRow(s *StatementScope, named binding, physical queryPhysicalRow, schema string, row []sqltype.Value, eval *EvalContext) error {
	if physical.leaf.ref == named.ref {
		return e.validateMutationRow(s, named, schema, row, eval)
	}
	relation := physical.leaf.relation
	generationEval := *eval
	if err := evaluateGenerations(eval.Context, relation, row, &generationEval); err != nil {
		return err
	}
	diagnostic := func(column, check string) error {
		detail, err := e.rowConstraintDetail(s, &named, physical.values(storage.RowVersion{Values: row}))
		if err != nil {
			return err
		}
		physicalSchema := schema
		e.mu.Lock()
		if ns := e.privateCatalogLocked(s.owner.catalog).Get(relation.Namespace()); ns != nil {
			physicalSchema = ns.Name()
		}
		e.mu.Unlock()
		if check != "" {
			return checkDetailError(physicalSchema, relation.Name(), check, detail)
		}
		return notNullDetailError(physicalSchema, relation.Name(), column, detail)
	}
	for i, column := range relation.Columns {
		if !column.Nullable {
			value, err := logicalColumnValue(relation, i, row, &generationEval)
			if err != nil {
				return err
			}
			if value.IsNull() {
				return diagnostic(column.Name, "")
			}
		}
	}
	if constraint, err := validateCheckRow(relation, row, &generationEval); err != nil {
		return err
	} else if constraint != nil {
		return diagnostic("", constraint.Name)
	}
	return nil
}

func scatterMutationRow(physical queryPhysicalRow, old storage.RowVersion, named []sqltype.Value, targets []int) []sqltype.Value {
	if physical.segment == nil || physical.segment.layout == nil {
		return named
	}
	row := append([]sqltype.Value(nil), old.Values...)
	for _, slot := range targets {
		row[physical.segment.layout[slot]] = named[slot]
	}
	return row
}
