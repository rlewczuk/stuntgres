package engine

import (
	"context"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
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
		path, err := lookupNamespacesSnapshot(ddlAuthorityOf(pending.Auth), definition.Raw.Target, analyze.RelationLookup)
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
			target, err := definition.ResolveTarget(base)
			if err != nil {
				e.mu.Unlock()
				return err
			}
			if err := table.StageForeignKey(ddlHost{e: e, op: op, stage: stage}, declaration.Auth, child, definition.Raw, target, definition.Supporting); err != nil {
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
	// The Restore scan reads the committed rows and index roots directly.
	capture := func(ref ids.ObjectRef, idx ids.ObjectAddress) (table.ForeignKeyRows, error) {
		store := e.storage.Relations[ref.Address]
		if store == nil {
			return table.ForeignKeyRows{}, ids.ErrCatalogReplaced
		}
		rows := table.ForeignKeyRows{Storage: store, Committed: store.Versions, Ends: store.Ends, Frontier: e.visibility.CommitSeq()}
		if idx != (ids.ObjectAddress{}) {
			rows.Index, rows.Root = idx, e.storage.IndexRoots[idx]
		}
		return rows, nil
	}
	for _, declaration := range pending {
		child := view.MustRelation(declaration.Child.Address)
		auth := ddlAuthorityOf(declaration.Auth)
		auth.Catalog = view
		auth.Recapture(auth.Catalog)
		scan := table.ForeignKeyScan{View: auth.View, Env: auth, Domain: e.catalog.Domain, Capture: capture}
		if err := table.ScanRestoredForeignKeys(ctx, scan, child, declaration.Definitions); err != nil {
			return err
		}
	}
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		return err
	}
	_, err = e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: base.Epoch()})
	return err
}
