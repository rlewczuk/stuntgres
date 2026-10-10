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

func captureAlterTypeIndexes(scope *StatementScope, catalog CatalogRead, original, survivor *relationDescriptor, actions []parser.AlterTableAction) ([]alterTypeIndex, error) {
	op := scope.op
	hasType := false
	for _, action := range actions {
		hasType = hasType || action.Action == parser.AlterColumnType
	}
	if !hasType {
		return nil, nil
	}
	var queue []alterTypeIndex
	for _, descriptor := range catalog.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.sqlIndex.parent != original.address {
			continue
		}
		pending := alterTypeIndex{index: index}
		dropped := false
		for _, key := range index.sqlIndex.keys {
			if key.derived == nil {
				dropped = dropped || !survivor.hasAttribute(key.attribute)
			} else {
				for _, dep := range key.derived.dependencies {
					a := dep.Address
					if a.ClassID == original.address.ClassID && a.ObjectID == original.address.ObjectID && a.SubID != 0 && !survivor.hasAttribute(a.SubID) {
						dropped = true
					}
				}
			}
		}
		if predicate := index.sqlIndex.predicate; predicate != nil {
			for _, dep := range predicate.dependencies {
				a := dep.Address
				if a.ClassID != original.address.ClassID || a.ObjectID != original.address.ObjectID || a.SubID == 0 {
					continue
				}
				dropped = dropped || !survivor.hasAttribute(a.SubID)
				for _, action := range actions {
					slot, exists := original.index[action.Column.Text]
					if exists && action.Action == parser.AlterColumnType && original.attributes[slot] == a.SubID {
						pending.predicate = true
					}
				}
			}
		}
		for _, attribute := range index.sqlIndex.include {
			dropped = dropped || !survivor.hasAttribute(attribute)
		}
		if dropped {
			continue
		}
		for i, key := range index.sqlIndex.keys {
			if key.derived == nil {
				continue
			}
			affected := false
			for _, action := range actions {
				if action.Action != parser.AlterColumnType {
					continue
				}
				slot, exists := original.index[action.Column.Text]
				if !exists {
					continue
				}
				for _, dep := range key.derived.dependencies {
					a := dep.Address
					if a.ClassID == original.address.ClassID && a.ObjectID == original.address.ObjectID && a.SubID == original.attributes[slot] {
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
			text, err := indexDefinitionSQL(op.Context(), catalog, index, indexPresentation{reconstruct: true, path: scope.deparsePath, output: scope.output})
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
		definition := *copy.sqlIndex
		definition.keys = append([]indexKey(nil), definition.keys...)
		copy.sqlIndex = &definition
		// This queue contains actual affected TYPE reconstructions, not generic
		// descriptor evolution. Publish these flags only with the validated
		// private candidate; later producers must enforce its rebuilt tuples.
		definition.ready, definition.valid, definition.live = true, true, true
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
			definition.predicate = predicate
		}
		for _, slot := range pending.keys {
			e.mu.Lock()
			derived, _, err := e.bindIndexExpression(scope.op, scope.input, auth, parent, syntax[0].(*parser.CreateIndex).Keys[slot].Expression, nil)
			e.mu.Unlock()
			if err != nil {
				return err
			}
			definition.keys[slot].derived = derived
		}
		oldParent := stage.base.state.descriptors[parent.address].(*relationDescriptor)
		for i, key := range definition.keys {
			oldUsage, err := indexKeyBaseUsage(oldParent, pending.index.sqlIndex.keys[i])
			if err != nil {
				return err
			}
			newUsage, err := indexKeyBaseUsage(parent, key)
			if err != nil {
				return err
			}
			definition.keys[i] = reconstructIndexKey(key, oldUsage, newUsage, definition.method)
		}
		// The captured TYPE queue already normalized every key against its
		// original Usage while retaining the newly rebound expression trees.
		next, err := evolveIndex(&copy, oldParent, parent, nil)
		if err != nil {
			return err
		}
		if next != nil {
			if next.revision == pending.index.revision {
				next.revision++
				if next.revision == 0 {
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
