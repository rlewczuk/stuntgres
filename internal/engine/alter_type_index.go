package engine

import (
	"fmt"
	"stuntgres/internal/parser"
)

type alterTypeIndex struct {
	index     *relationDescriptor
	keys      []int
	predicate bool
	sql       string
}

func captureAlterTypeIndexes(scope *StatementScope, view CatalogRead, original, survivor *relationDescriptor, actions []parser.AlterTableAction) ([]alterTypeIndex, error) {
	op := scope.op
	hasType := false
	for _, action := range actions {
		hasType = hasType || action.Action == parser.AlterColumnType
	}
	if !hasType {
		return nil, nil
	}
	var queue []alterTypeIndex
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != original.Address() {
			continue
		}
		pending := alterTypeIndex{index: index}
		dropped := false
		for _, key := range index.SQLIndex.Keys {
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
		if predicate := index.SQLIndex.Predicate; predicate != nil {
			for _, dep := range predicate.Dependencies {
				a := dep.Address
				if a.ClassID != original.Address().ClassID || a.ObjectID != original.Address().ObjectID || a.SubID == 0 {
					continue
				}
				dropped = dropped || !survivor.HasAttribute(a.SubID)
				for _, action := range actions {
					slot, exists := original.Index[action.Column.Text]
					if exists && action.Action == parser.AlterColumnType && original.Attributes[slot] == a.SubID {
						pending.predicate = true
					}
				}
			}
		}
		for _, attribute := range index.SQLIndex.Include {
			dropped = dropped || !survivor.HasAttribute(attribute)
		}
		if dropped {
			continue
		}
		for i, key := range index.SQLIndex.Keys {
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
			pending.keys = append(pending.keys, i)
		}
		if len(pending.keys) > 0 || pending.predicate {
			text, err := indexDefinitionSQL(op.Context(), view, index, indexPresentation{reconstruct: true, path: scope.deparsePath, output: scope.output})
			if err != nil {
				return nil, err
			}
			pending.sql = text
			queue = append(queue, pending)
		}
	}
	return queue, nil
}

func (e *Engine) rebindAlterTypeIndexes(scope *StatementScope, auth AuthorizationView, parent *relationDescriptor, stage *CatalogStage, queue []alterTypeIndex) error {
	for _, pending := range queue {
		copy := *pending.index
		definition := *copy.SQLIndex
		definition.Keys = append([]indexKey(nil), definition.Keys...)
		copy.SQLIndex = &definition
		// This queue contains actual affected TYPE reconstructions, not generic
		// descriptor evolution. Publish these flags only with the validated
		// private candidate; later producers must enforce its rebuilt tuples.
		definition.Ready, definition.Valid, definition.Live = true, true, true
		syntax, err := parser.ParseWithOptions(pending.sql, parser.Options{Context: scope.op.Context(), StandardConformingStrings: true})
		if err != nil {
			return err
		}
		if pending.predicate {
			e.mu.Lock()
			predicate, err := e.bindIndexPredicate(scope.op, scope.input, auth, parent, syntax[0].(*parser.CreateIndex).Predicate)
			e.mu.Unlock()
			if err != nil {
				return err
			}
			definition.Predicate = predicate
		}
		for _, slot := range pending.keys {
			e.mu.Lock()
			derived, _, err := e.bindIndexExpression(scope.op, scope.input, auth, parent, syntax[0].(*parser.CreateIndex).Keys[slot].Expression, nil)
			e.mu.Unlock()
			if err != nil {
				return err
			}
			definition.Keys[slot].Derived = derived
		}
		oldParent := stage.Base.MustRelation(parent.Address())
		for i, key := range definition.Keys {
			oldUsage, err := indexKeyBaseUsage(oldParent, pending.index.SQLIndex.Keys[i])
			if err != nil {
				return err
			}
			newUsage, err := indexKeyBaseUsage(parent, key)
			if err != nil {
				return err
			}
			definition.Keys[i] = reconstructIndexKey(key, oldUsage, newUsage, definition.Method)
		}
		// The captured TYPE queue already normalized every key against its
		// original Usage while retaining the newly rebound expression trees.
		next, err := evolveIndex(&copy, oldParent, parent, nil)
		if err != nil {
			return err
		}
		if next != nil {
			if next.Revision() == pending.index.Revision() {
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
