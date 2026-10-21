package engine

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Pending declarations live only inside the isolated Restore invocation. Capture
// the declaring principal and namespace identities, not the later owner directive.
func (e *Engine) captureRestoredForeignKeys(op *session.Operation, raw []parser.ForeignKeyConstraint) (table.RestoredForeignKeys, error) {
	auth, err := operationAuthorization(op)
	if err != nil {
		return table.RestoredForeignKeys{}, err
	}
	pending := table.RestoredForeignKeys{Auth: auth, Definitions: make([]table.RestoredForeignKey, len(raw))}
	for i, f := range raw {
		pending.Definitions[i].Raw = f
	}
	return pending, nil
}

// Only the exact successful creation outcome admits target/path discovery.
// A skipped CREATE must not inspect any ignored declaration, even its schema.
func (e *Engine) resolveRestoredForeignKeyTargets(pending *table.RestoredForeignKeys) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range pending.Definitions {
		definition := &pending.Definitions[i]
		path, err := e.lookupNamespacesSnapshot(ddlAuthorityOf(pending.Auth), definition.Raw.Target, analyze.RelationLookup)
		if err != nil {
			return err
		}
		definition.Path = make([]ids.ObjectAddress, path.Count())
		for at := range path.Count() {
			namespace := path.At(at)
			definition.Path[at] = namespace
			if definition.Target == (ids.ObjectAddress{}) {
				definition.Target = ddlAuthorityOf(pending.Auth).Catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: definition.Raw.Target.Parts[len(definition.Raw.Target.Parts)-1].Text})
			}
		}
	}
	return nil
}

func (e *Engine) bindRestoredForeignKeys(ctx context.Context, op *session.Operation, pending []table.RestoredForeignKeys) error {
	if len(pending) == 0 {
		return nil
	}
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	e.mu.Lock()
	for _, declaration := range pending {
		if declaration.Child.Domain() != e.catalog.Domain || declaration.Child.Epoch != base.Epoch() {
			e.mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		child, ok := stage.Get(declaration.Child.Address).(*catalog.Relation)
		if !ok {
			old, ok := base.Relation(declaration.Child.Address)
			if !ok {
				e.mu.Unlock()
				return ids.ErrCatalogReplaced
			}
			copy := *old
			copy.Constraints.ForeignKeys = slices.Clone(old.Constraints.ForeignKeys)
			child = &copy
			stage.UpdateDescriptor(child)
		}
		for i := range declaration.Definitions {
			definition := &declaration.Definitions[i]
			if definition.Inherited != nil {
				if err := validateRestoredInheritedForeignKey(e, definition.Inherited, op, declaration, base); err != nil {
					e.mu.Unlock()
					return err
				}
			}
			target := definition.Target
			if target == (ids.ObjectAddress{}) {
				for _, namespace := range definition.Path {
					target = base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: definition.Raw.Target.Parts[len(definition.Raw.Target.Parts)-1].Text})
					if target != (ids.ObjectAddress{}) {
						break
					}
				}
			}
			if target == (ids.ObjectAddress{}) {
				e.mu.Unlock()
				return sqlError("42P01", fmt.Sprintf(`relation "%s" does not exist`, catalog.ObjectNameText(definition.Raw.Target)))
			}
			if _, ok := base.Find(target); !ok {
				e.mu.Unlock()
				return ids.ErrCatalogReplaced
			}
			if err := e.stageForeignKey(ddlAuthorityOf(declaration.Auth), stage, child, definition.Raw, target, definition.Supporting); err != nil {
				e.mu.Unlock()
				return err
			}
			definition.BoundSlot = len(child.Constraints.ForeignKeys) - 1
			bound := &child.Constraints.ForeignKeys[definition.BoundSlot]
			definition.BoundOID = bound.OID
			if definition.Inherited != nil && !definition.Inherited.Accepts(bound) {
				e.mu.Unlock()
				return ids.ErrCatalogReplaced
			}
		}
	}
	e.mu.Unlock()
	view := e.stageRead(stage)
	for _, declaration := range pending {
		child := view.MustRelation(declaration.Child.Address)
		auth := ddlAuthorityOf(declaration.Auth)
		auth.Catalog = view
		auth.Recapture(auth.Catalog)
		for _, definition := range declaration.Definitions {
			if definition.BoundSlot < 0 || definition.BoundSlot >= len(child.Constraints.ForeignKeys) || definition.BoundOID == 0 || child.Constraints.ForeignKeys[definition.BoundSlot].OID != definition.BoundOID {
				return ids.ErrCatalogReplaced
			}
			f := child.Constraints.ForeignKeys[definition.BoundSlot]
			if f.NotValid {
				continue
			}
			parent := view.MustRelation(f.Target)
			index := view.MustRelation(f.Index)
			comparisons, err := table.BindForeignKeyComparison(view, child, parent, index, f)
			if err != nil {
				return err
			}
			childStore, parentStore := e.storage.Relations[child.Address()], e.storage.Relations[parent.Address()]
			if childStore == nil || parentStore == nil {
				return ids.ErrCatalogReplaced
			}
			children := table.ForeignKeyRows{Storage: childStore, Committed: childStore.Versions, Ends: childStore.Ends, Frontier: e.visibility.CommitSeq()}
			parents := table.ForeignKeyRows{Storage: parentStore, Committed: parentStore.Versions, Ends: parentStore.Ends, Frontier: e.visibility.CommitSeq(), Index: f.Index, Root: e.storage.IndexRoots[f.Index]}
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

func validateForeignKeyData(ctx context.Context, auth AuthorizationView, child, parent *catalog.Relation, f catalog.ForeignKeyConstraint, comparisons []table.ForeignKeyComparison, children, parents table.ForeignKeyRows, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) error {
	index, ok := auth.Catalog.Relation(f.Index)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	key := make([]sqltype.Value, len(comparisons))
	return children.Each(func(row storage.RowVersion) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		any, all := table.ForeignKeyNullState(comparisons, row.Values)
		if any {
			if f.Full && !all {
				return false, table.ForeignKeyDiagnostic(ctx, auth.View, auth, child, parent, f, row.Values, false, true, output)
			}
			return false, nil
		}
		if err := table.PrepareForeignKeyChild(ctx, comparisons, row.Values, key, input); err != nil {
			return false, err
		}
		matching, _, err := table.FindForeignKeyParent(ctx, index, comparisons, parents, key, input)
		if err != nil {
			return false, err
		}
		found := matching.Row != 0
		if !found {
			return false, table.ForeignKeyDiagnostic(ctx, auth.View, auth, child, parent, f, row.Values, false, false, output)
		}
		return false, nil
	})
}
