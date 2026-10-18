package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type restoredForeignKey struct {
	raw        parser.ForeignKeyConstraint
	path       []ObjectAddress
	target     ObjectAddress
	supporting ObjectAddress // Exact captured primary for physical self-FK or omitted-reference inherited replay.
	inherited  *restoredInheritedForeignKey
	boundOID   OID
	boundSlot  int // Actual append position in this sole stage, verified against boundOID.
}
type restoredForeignKeys struct {
	child       ObjectRef
	auth        AuthorizationView
	definitions []restoredForeignKey
}

// Pending declarations live only inside the isolated Restore invocation. Capture
// the declaring principal and namespace identities, not the later owner directive.
func (e *Engine) captureRestoredForeignKeys(op *Operation, raw []parser.ForeignKeyConstraint) (restoredForeignKeys, error) {
	auth, err := op.Authorization()
	if err != nil {
		return restoredForeignKeys{}, err
	}
	pending := restoredForeignKeys{auth: auth, definitions: make([]restoredForeignKey, len(raw))}
	for i, f := range raw {
		pending.definitions[i].raw = f
	}
	return pending, nil
}

// Only the exact successful creation outcome admits target/path discovery.
// A skipped CREATE must not inspect any ignored declaration, even its schema.
func (e *Engine) resolveRestoredForeignKeyTargets(pending *restoredForeignKeys) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range pending.definitions {
		definition := &pending.definitions[i]
		path, err := e.lookupNamespacesSnapshot(pending.auth, definition.raw.Target, relationLookup)
		if err != nil {
			return err
		}
		definition.path = make([]ObjectAddress, path.count())
		for at := range path.count() {
			namespace := path.at(at)
			definition.path[at] = namespace
			if definition.target == (ObjectAddress{}) {
				definition.target = pending.auth.catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: namespace, Name: definition.raw.Target.Parts[len(definition.raw.Target.Parts)-1].Text}]
			}
		}
	}
	return nil
}

func stripRestoreForeignKeys(raw *parser.CreateTable) {
	elements := raw.Elements[:0]
	for _, element := range raw.Elements {
		if element.ForeignKey != nil {
			continue
		}
		if element.Column != nil {
			constraints := element.Column.Constraints[:0]
			for _, constraint := range element.Column.Constraints {
				if constraint.ForeignKey == nil {
					constraints = append(constraints, constraint)
				}
			}
			element.Column.Constraints = constraints
		}
		elements = append(elements, element)
	}
	raw.Elements = elements
}

func (e *Engine) bindRestoredForeignKeys(ctx context.Context, op *Operation, pending []restoredForeignKeys) error {
	if len(pending) == 0 {
		return nil
	}
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	e.mu.Lock()
	for _, declaration := range pending {
		if declaration.child.owner != e.domain || declaration.child.Epoch != base.Epoch() {
			e.mu.Unlock()
			return ErrCatalogReplaced
		}
		child, ok := stage.descriptors[declaration.child.Address].(*relationDescriptor)
		if !ok {
			old, ok := base.state.descriptors[declaration.child.Address].(*relationDescriptor)
			if !ok {
				e.mu.Unlock()
				return ErrCatalogReplaced
			}
			copy := *old
			copy.foreignKeys = slices.Clone(old.foreignKeys)
			child = &copy
			stage.descriptors[child.address] = child
		}
		for i := range declaration.definitions {
			definition := &declaration.definitions[i]
			if definition.inherited != nil {
				if err := definition.inherited.validate(e, op, declaration, base); err != nil {
					e.mu.Unlock()
					return err
				}
			}
			target := definition.target
			if target == (ObjectAddress{}) {
				for _, namespace := range definition.path {
					target = base.state.names[NameKey{ClassID: RelationClassID, Namespace: namespace, Name: definition.raw.Target.Parts[len(definition.raw.Target.Parts)-1].Text}]
					if target != (ObjectAddress{}) {
						break
					}
				}
			}
			if target == (ObjectAddress{}) {
				e.mu.Unlock()
				return sqlError("42P01", fmt.Sprintf(`relation "%s" does not exist`, objectNameText(definition.raw.Target)))
			}
			if _, ok := base.state.descriptors[target]; !ok {
				e.mu.Unlock()
				return ErrCatalogReplaced
			}
			if err := e.stageForeignKey(declaration.auth, stage, child, definition.raw, target, definition.supporting); err != nil {
				e.mu.Unlock()
				return err
			}
			definition.boundSlot = len(child.foreignKeys) - 1
			bound := &child.foreignKeys[definition.boundSlot]
			definition.boundOID = bound.OID
			if definition.inherited != nil && !definition.inherited.accepts(bound) {
				e.mu.Unlock()
				return ErrCatalogReplaced
			}
		}
	}
	e.mu.Unlock()
	view := e.stageRead(stage)
	for _, declaration := range pending {
		child := view.state.descriptors[declaration.child.Address].(*relationDescriptor)
		auth := declaration.auth
		auth.catalog = view
		auth.recaptureCatalog(auth.catalog)
		for _, definition := range declaration.definitions {
			if definition.boundSlot < 0 || definition.boundSlot >= len(child.foreignKeys) || definition.boundOID == 0 || child.foreignKeys[definition.boundSlot].OID != definition.boundOID {
				return ErrCatalogReplaced
			}
			f := child.foreignKeys[definition.boundSlot]
			if f.NotValid {
				continue
			}
			parent := view.state.descriptors[f.Target].(*relationDescriptor)
			index := view.state.descriptors[f.Index].(*relationDescriptor)
			comparisons, err := bindForeignKeyComparison(view, child, parent, index, f)
			if err != nil {
				return err
			}
			childStore, parentStore := e.storage[child.address], e.storage[parent.address]
			if childStore == nil || parentStore == nil {
				return ErrCatalogReplaced
			}
			children := foreignKeyRows{storage: childStore, committed: childStore.versions, ends: childStore.ends, frontier: e.commitSeq}
			parents := foreignKeyRows{storage: parentStore, committed: parentStore.versions, ends: parentStore.ends, frontier: e.commitSeq, index: f.Index, root: e.indexRoots[f.Index]}
			if err := validateForeignKeyData(ctx, auth, child, parent, f, comparisons, children, parents, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}); err != nil {
				return err
			}
		}
	}
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		return err
	}
	_, err = e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: base.Epoch()})
	return err
}

func validateForeignKeyData(ctx context.Context, auth AuthorizationView, child, parent *relationDescriptor, f foreignKeyConstraint, comparisons []foreignKeyComparison, children, parents foreignKeyRows, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) error {
	index, ok := auth.catalog.state.descriptors[f.Index].(*relationDescriptor)
	if !ok {
		return ErrCatalogReplaced
	}
	key := make([]sqltype.Value, len(comparisons))
	return children.each(func(row rowVersion) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		any, all := foreignKeyNullState(comparisons, row.values)
		if any {
			if f.Full && !all {
				return false, foreignKeyDiagnostic(ctx, auth, child, parent, f, row.values, false, true, output)
			}
			return false, nil
		}
		if err := prepareForeignKeyChild(ctx, comparisons, row.values, key, input); err != nil {
			return false, err
		}
		matching, _, err := findForeignKeyParent(ctx, index, comparisons, parents, key, input)
		if err != nil {
			return false, err
		}
		found := matching.row != 0
		if !found {
			return false, foreignKeyDiagnostic(ctx, auth, child, parent, f, row.values, false, false, output)
		}
		return false, nil
	})
}
