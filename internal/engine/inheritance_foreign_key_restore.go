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
		return restoredForeignKeys{}, false, sqlError("0A000", RestoreInheritedForeignKeyBoundary)
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
	auth := ddlAuthorityOf(pending.Auth)
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
	if !sourceOK || !targetOK || created[sourceRef.Address] != sourceRef || created[targetRef.Address] != targetRef || source.RelationKind != catalog.TableRelation || target.RelationKind != catalog.TableRelation || source.Partition != nil || source.Membership != nil || target.Partition != nil || target.Membership != nil || sourceRef == targetRef || !InheritedForeignKeyDescendant(auth.Catalog, target, source.Address()) {
		return reject()
	}
	if auth.Catalog.MustNamespace(source.Namespace()).Temporary != (ids.SessionToken{}) || auth.Catalog.MustNamespace(target.Namespace()).Temporary != (ids.SessionToken{}) {
		return reject()
	}
	if err := auth.Check(access.Requirement{Object: sourceRef, OwnerOnly: true}); err != nil {
		return restoredForeignKeys{}, false, err
	}
	receipt := &restoredInheritedForeignKey{Source: sourceRef, Target: targetRef, BeforeSource: source, BeforeTarget: target, Raw: *foreign, Providers: make(map[ids.ObjectAddress]restoredInheritedForeignIndex)}
	var valid bool
	receipt.Columns, valid = CaptureInheritedForeignColumns(source, foreign.Columns)
	if !valid {
		return reject()
	}
	receipt.Deleted, valid = CaptureInheritedForeignColumns(source, foreign.DeleteColumns)
	if !valid {
		return reject()
	}
	referenced := foreign.Referenced
	if len(referenced) == 0 {
		for _, key := range target.Constraints.Unique {
			if key.Primary {
				pending.Definitions[0].Supporting = key.Index
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
	receipt.Referenced, valid = CaptureInheritedForeignColumns(target, referenced)
	if !valid || len(receipt.Referenced) != len(receipt.Columns) {
		return reject()
	}
	attributes := make([]int32, len(receipt.Referenced))
	for i, column := range receipt.Referenced {
		attributes[i] = column.Attribute
	}
	for address, descriptor := range auth.Catalog.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != target.Address() {
			continue
		}
		eligible, err := ForeignKeyIndexEligible(target, index, attributes)
		if err != nil {
			return restoredForeignKeys{}, false, err
		}
		receipt.Providers[address] = restoredInheritedForeignIndex{Current: index, Eligible: eligible}
	}
	pending.Child = sourceRef
	pending.Definitions[0].Target = targetRef.Address
	pending.Definitions[0].Path = []ids.ObjectAddress{target.Namespace()}
	pending.Definitions[0].Inherited = receipt
	return pending, true, nil
}

// Comments and final attribute-rank repairs do not alter these key/provider
// observations. No later key, owner, type or provider incarnation can reuse them.
func validateRestoredInheritedForeignKey(e *Engine, receipt *restoredInheritedForeignKey, op *session.Operation, declaration restoredForeignKeys, view catalog.Read) error {
	auth := ddlAuthorityOf(declaration.Auth)
	auth.Catalog = view
	auth.Recapture(view)
	if receipt.Source != declaration.Child || receipt.Source.Domain() != e.catalog.Domain || receipt.Target.Domain() != e.catalog.Domain || receipt.Source.Epoch != view.Epoch() || receipt.Target.Epoch != view.Epoch() || auth.RoleRecord != e.roles.published || !partitionConstraintReplayImport(e, op, auth) {
		return ids.ErrCatalogReplaced
	}
	source, _ := view.Relation(receipt.Source.Address)
	target, _ := view.Relation(receipt.Target.Address)
	if !InheritedForeignRelationUnchanged(source, receipt.BeforeSource) || !InheritedForeignRelationUnchanged(target, receipt.BeforeTarget) || !InheritedForeignKeyDescendant(view, target, source.Address()) || !InheritedForeignColumnsUnchanged(source, receipt.Columns) || !InheritedForeignColumnsUnchanged(source, receipt.Deleted) || !InheritedForeignColumnsUnchanged(target, receipt.Referenced) {
		return ids.ErrCatalogReplaced
	}
	seen := 0
	for address, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != target.Address() {
			continue
		}
		observed, exists := receipt.Providers[address]
		if !exists || index.SQLIndex != observed.Current.SQLIndex || index.Owner != observed.Current.Owner || index.Namespace() != observed.Current.Namespace() || index.Name() != observed.Current.Name() || len(index.Columns) != len(observed.Current.Columns) {
			return ids.ErrCatalogReplaced
		}
		for i, column := range index.Columns {
			if column.Usage != observed.Current.Columns[i].Usage {
				return ids.ErrCatalogReplaced
			}
		}
		seen++
	}
	if seen != len(receipt.Providers) {
		return ids.ErrCatalogReplaced
	}
	return auth.Check(access.Requirement{Object: receipt.Source, OwnerOnly: true})
}
