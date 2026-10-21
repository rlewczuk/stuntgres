package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func captureAlterTypeIndexes(scope *StatementScope, view catalog.Read, original, survivor *catalog.Relation, actions []parser.AlterTableAction) ([]table.AlterTypeIndex, error) {
	op := scope.op
	hasType := false
	for _, action := range actions {
		hasType = hasType || action.Action == parser.AlterColumnType
	}
	if !hasType {
		return nil, nil
	}
	var queue []table.AlterTypeIndex
	for _, descriptor := range view.Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || idx.SQLIndex.Parent != original.Address() {
			continue
		}
		pending := table.AlterTypeIndex{Index: idx}
		dropped := false
		for _, key := range idx.SQLIndex.Keys {
			if key.Derived == nil {
				dropped = dropped || !survivor.HasAttribute(key.Attribute)
			} else {
				for _, dep := range key.Derived.Dependencies {
					a := dep.Address
					if a.ClassID == original.Address().ClassID && a.ObjectID == original.Address().ObjectID && a.SubID != 0 && !survivor.HasAttribute(a.SubID) {
						dropped = true
					}
				}
			}
		}
		if predicate := idx.SQLIndex.Predicate; predicate != nil {
			for _, dep := range predicate.Dependencies {
				a := dep.Address
				if a.ClassID != original.Address().ClassID || a.ObjectID != original.Address().ObjectID || a.SubID == 0 {
					continue
				}
				dropped = dropped || !survivor.HasAttribute(a.SubID)
				for _, action := range actions {
					slot, exists := original.Index[action.Column.Text]
					if exists && action.Action == parser.AlterColumnType && original.Attributes[slot] == a.SubID {
						pending.Predicate = true
					}
				}
			}
		}
		for _, attribute := range idx.SQLIndex.Include {
			dropped = dropped || !survivor.HasAttribute(attribute)
		}
		if dropped {
			continue
		}
		for i, key := range idx.SQLIndex.Keys {
			if key.Derived == nil {
				continue
			}
			affected := false
			for _, action := range actions {
				if action.Action != parser.AlterColumnType {
					continue
				}
				slot, exists := original.Index[action.Column.Text]
				if !exists {
					continue
				}
				for _, dep := range key.Derived.Dependencies {
					a := dep.Address
					if a.ClassID == original.Address().ClassID && a.ObjectID == original.Address().ObjectID && a.SubID == original.Attributes[slot] {
						affected = true
					}
				}
			}
			if !affected {
				continue
			}
			pending.Keys = append(pending.Keys, i)
		}
		if len(pending.Keys) > 0 || pending.Predicate {
			text, err := index.DefinitionSQL(op.Context(), view, idx, index.Presentation{Reconstruct: true, Path: scope.inquiry.deparsePath, Output: scope.output})
			if err != nil {
				return nil, err
			}
			pending.SQL = text
			queue = append(queue, pending)
		}
	}
	return queue, nil
}

func (e *Engine) rebindAlterTypeIndexes(scope *StatementScope, auth AuthorizationView, parent *catalog.Relation, stage *CatalogStage, queue []table.AlterTypeIndex) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, pending := range queue {
		copy := *pending.Index
		definition := *copy.SQLIndex
		definition.Keys = append([]catalog.IndexKey(nil), definition.Keys...)
		copy.SQLIndex = &definition
		// This queue contains actual affected TYPE reconstructions, not generic
		// descriptor evolution. Publish these flags only with the validated
		// private candidate; later producers must enforce its rebuilt tuples.
		definition.Ready, definition.Valid, definition.Live = true, true, true
		syntax, err := parser.ParseWithOptions(pending.SQL, parser.Options{Context: scope.op.Context(), StandardConformingStrings: true})
		if err != nil {
			return err
		}
		if pending.Predicate {
			predicate, err := e.bindIndexPredicate(scope.op, scope.input, auth, parent, syntax[0].(*parser.CreateIndex).Predicate)
			if err != nil {
				return err
			}
			definition.Predicate = predicate
		}
		for _, slot := range pending.Keys {
			derived, _, err := e.bindIndexExpression(scope.op, scope.input, auth, parent, syntax[0].(*parser.CreateIndex).Keys[slot].Expression, nil)
			if err != nil {
				return err
			}
			definition.Keys[slot].Derived = derived
		}
		oldParent := stage.Base.MustRelation(parent.Address())
		for i, key := range definition.Keys {
			oldUsage, err := index.KeyBaseUsage(oldParent, pending.Index.SQLIndex.Keys[i])
			if err != nil {
				return err
			}
			newUsage, err := index.KeyBaseUsage(parent, key)
			if err != nil {
				return err
			}
			definition.Keys[i] = index.ReconstructIndexKey(key, oldUsage, newUsage, definition.Method)
		}
		// The captured TYPE queue already normalized every key against its
		// original Usage while retaining the newly rebound expression trees.
		next, err := index.EvolveIndex(&copy, oldParent, parent, nil)
		if err != nil {
			return err
		}
		if next != nil {
			if next.Revision() == pending.Index.Revision() {
				next.ObjectRevision++
				if next.Revision() == 0 {
					return fmt.Errorf("stuntgres: index revision exhausted")
				}
			}
			if err = stage.Replace(next); err != nil {
				return err
			}
		}
	}
	return nil
}
