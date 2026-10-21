package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// This is not a general valid ALTER whitelist. Only a successful isolated
// import of both actual relations, with a strict ordinary inheritance edge,
// can issue a receipt. The incumbent pending binder remains the sole FK binder.
func (e *Engine) captureInheritedForeignKey(op *session.Operation, raw *parser.AlterTable, created map[ids.ObjectAddress]ids.ObjectRef) (restoredForeignKeys, bool, error) {
	if len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddForeignKey || raw.Actions[0].ForeignKey == nil || raw.Actions[0].ForeignKey.NotValid {
		return restoredForeignKeys{}, false, nil
	}
	reject := func() (restoredForeignKeys, bool, error) {
		return restoredForeignKeys{}, false, sqlError("0A000", restoreInheritedForeignKeyBoundary)
	}
	foreign := raw.Actions[0].ForeignKey
	if !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(foreign.Target.Parts) != 2 || foreign.Name.Text == "" || len(foreign.Columns) == 0 {
		return reject()
	}
	if raw.Name.Parts[0].Text == foreign.Target.Parts[0].Text && raw.Name.Parts[1].Text == foreign.Target.Parts[1].Text {
		return restoredForeignKeys{}, false, nil
	}
	pending, err := e.captureRestoredForeignKeys(op, []parser.ForeignKeyConstraint{*foreign})
	if err != nil {
		return restoredForeignKeys{}, false, err
	}
	auth := ddlAuthorityOf(pending.auth)
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) {
		return reject()
	}
	sourceRef, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return restoredForeignKeys{}, false, err
	}
	targetRef, err := e.resolveRelationReference(auth, foreign.Target, false)
	if err != nil {
		return restoredForeignKeys{}, false, err
	}
	source, sourceOK := auth.Catalog.Relation(sourceRef.Address)
	target, targetOK := auth.Catalog.Relation(targetRef.Address)
	if !sourceOK || !targetOK || created[sourceRef.Address] != sourceRef || created[targetRef.Address] != targetRef || source.RelationKind != catalog.TableRelation || target.RelationKind != catalog.TableRelation || source.Partition != nil || source.Membership != nil || target.Partition != nil || target.Membership != nil || sourceRef == targetRef || !inheritedForeignKeyDescendant(auth.Catalog, target, source.Address()) {
		return reject()
	}
	if auth.Catalog.MustNamespace(source.Namespace()).Temporary != (ids.SessionToken{}) || auth.Catalog.MustNamespace(target.Namespace()).Temporary != (ids.SessionToken{}) {
		return reject()
	}
	if err := auth.Check(access.Requirement{Object: sourceRef, OwnerOnly: true}); err != nil {
		return restoredForeignKeys{}, false, err
	}
	receipt := &restoredInheritedForeignKey{source: sourceRef, target: targetRef, beforeSource: source, beforeTarget: target, raw: *foreign, providers: make(map[ids.ObjectAddress]restoredInheritedForeignIndex)}
	var valid bool
	receipt.columns, valid = captureInheritedForeignColumns(source, foreign.Columns)
	if !valid {
		return reject()
	}
	receipt.deleted, valid = captureInheritedForeignColumns(source, foreign.DeleteColumns)
	if !valid {
		return reject()
	}
	referenced := foreign.Referenced
	if len(referenced) == 0 {
		for _, key := range target.Constraints.Unique {
			if key.Primary {
				pending.definitions[0].supporting = key.Index
				referenced = make([]parser.Identifier, len(key.Attributes))
				for i, attribute := range key.Attributes {
					slot := target.IndexForAttribute(attribute)
					if slot < 0 {
						return reject()
					}
					referenced[i] = parser.Identifier{Text: target.Columns[slot].Name}
				}
				break
			}
		}
	}
	receipt.referenced, valid = captureInheritedForeignColumns(target, referenced)
	if !valid || len(receipt.referenced) != len(receipt.columns) {
		return reject()
	}
	attributes := make([]int32, len(receipt.referenced))
	for i, column := range receipt.referenced {
		attributes[i] = column.attribute
	}
	for address, descriptor := range auth.Catalog.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != target.Address() {
			continue
		}
		eligible, err := foreignKeyIndexEligible(target, index, attributes)
		if err != nil {
			return restoredForeignKeys{}, false, err
		}
		receipt.providers[address] = restoredInheritedForeignIndex{current: index, eligible: eligible}
	}
	pending.child = sourceRef
	pending.definitions[0].target = targetRef.Address
	pending.definitions[0].path = []ids.ObjectAddress{target.Namespace()}
	pending.definitions[0].inherited = receipt
	return pending, true, nil
}

// Comments and final attribute-rank repairs do not alter these key/provider
// observations. No later key, owner, type or provider incarnation can reuse them.
func validateRestoredInheritedForeignKey(e *Engine, receipt *restoredInheritedForeignKey, op *session.Operation, declaration restoredForeignKeys, view catalog.Read) error {
	auth := ddlAuthorityOf(declaration.auth)
	auth.Catalog = view
	auth.Recapture(view)
	if receipt.source != declaration.child || receipt.source.Domain() != e.catalog.Domain || receipt.target.Domain() != e.catalog.Domain || receipt.source.Epoch != view.Epoch() || receipt.target.Epoch != view.Epoch() || auth.RoleRecord != e.roles.published || !partitionConstraintReplayImport(e, op, auth) {
		return ids.ErrCatalogReplaced
	}
	source, _ := view.Relation(receipt.source.Address)
	target, _ := view.Relation(receipt.target.Address)
	if !inheritedForeignRelationUnchanged(source, receipt.beforeSource) || !inheritedForeignRelationUnchanged(target, receipt.beforeTarget) || !inheritedForeignKeyDescendant(view, target, source.Address()) || !inheritedForeignColumnsUnchanged(source, receipt.columns) || !inheritedForeignColumnsUnchanged(source, receipt.deleted) || !inheritedForeignColumnsUnchanged(target, receipt.referenced) {
		return ids.ErrCatalogReplaced
	}
	seen := 0
	for address, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != target.Address() {
			continue
		}
		observed, exists := receipt.providers[address]
		if !exists || index.SQLIndex != observed.current.SQLIndex || index.Owner != observed.current.Owner || index.Namespace() != observed.current.Namespace() || index.Name() != observed.current.Name() || len(index.Columns) != len(observed.current.Columns) {
			return ids.ErrCatalogReplaced
		}
		for i, column := range index.Columns {
			if column.Usage != observed.current.Columns[i].Usage {
				return ids.ErrCatalogReplaced
			}
		}
		seen++
	}
	if seen != len(receipt.providers) {
		return ids.ErrCatalogReplaced
	}
	return auth.Check(access.Requirement{Object: receipt.source, OwnerOnly: true})
}
