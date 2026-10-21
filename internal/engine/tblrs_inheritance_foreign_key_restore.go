package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// InheritedForeignKeyShape reports whether raw is a valid, named foreign
// key between two different qualified relations, the only shape an
// ancestor/descendant receipt covers; a self reference or another action is
// not handled, a malformed one rejected.
func InheritedForeignKeyShape(raw *parser.AlterTable) (bool, error) {
	if len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddForeignKey || raw.Actions[0].ForeignKey == nil || raw.Actions[0].ForeignKey.NotValid {
		return false, nil
	}
	foreign := raw.Actions[0].ForeignKey
	if !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(foreign.Target.Parts) != 2 || foreign.Name.Text == "" || len(foreign.Columns) == 0 {
		return false, sqlError("0A000", table.RestoreInheritedForeignKeyBoundary)
	}
	if raw.Name.Parts[0].Text == foreign.Target.Parts[0].Text && raw.Name.Parts[1].Text == foreign.Target.Parts[1].Text {
		return false, nil
	}
	return true, nil
}

// CaptureInheritedForeignKey issues the receipt for foreign between
// sourceRef and targetRef, two freshly imported permanent ordinary tables
// with a strict inheritance edge, and binds pending's one declaration to it.
// This is not a general valid ALTER whitelist; the source's owner must
// declare it, and every index of the target is recorded as observed.
func CaptureInheritedForeignKey(view access.View, env access.Environment, sourceRef, targetRef ids.ObjectRef, created map[ids.ObjectAddress]ids.ObjectRef, foreign parser.ForeignKeyConstraint, pending *table.RestoredForeignKeys) error {
	reject := func() error { return sqlError("0A000", table.RestoreInheritedForeignKeyBoundary) }
	source, sourceOK := view.Catalog.Relation(sourceRef.Address)
	target, targetOK := view.Catalog.Relation(targetRef.Address)
	if !sourceOK || !targetOK || created[sourceRef.Address] != sourceRef || created[targetRef.Address] != targetRef || source.RelationKind != catalog.TableRelation || target.RelationKind != catalog.TableRelation || source.Partition != nil || source.Membership != nil || target.Partition != nil || target.Membership != nil || sourceRef == targetRef || !table.InheritedForeignKeyDescendant(view.Catalog, target, source.Address()) {
		return reject()
	}
	if view.Catalog.MustNamespace(source.Namespace()).Temporary != (ids.SessionToken{}) || view.Catalog.MustNamespace(target.Namespace()).Temporary != (ids.SessionToken{}) {
		return reject()
	}
	if err := view.Check(env, access.Requirement{Object: sourceRef, OwnerOnly: true}); err != nil {
		return err
	}
	receipt := &table.RestoredInheritedForeignKey{Source: sourceRef, Target: targetRef, BeforeSource: source, BeforeTarget: target, Raw: foreign, Providers: make(map[ids.ObjectAddress]table.RestoredInheritedForeignIndex)}
	var valid bool
	receipt.Columns, valid = table.CaptureInheritedForeignColumns(source, foreign.Columns)
	if !valid {
		return reject()
	}
	receipt.Deleted, valid = table.CaptureInheritedForeignColumns(source, foreign.DeleteColumns)
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
	receipt.Referenced, valid = table.CaptureInheritedForeignColumns(target, referenced)
	if !valid || len(receipt.Referenced) != len(receipt.Columns) {
		return reject()
	}
	attributes := make([]int32, len(receipt.Referenced))
	for i, column := range receipt.Referenced {
		attributes[i] = column.Attribute
	}
	for address, descriptor := range view.Catalog.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != target.Address() {
			continue
		}
		eligible, err := table.ForeignKeyIndexEligible(target, index, attributes)
		if err != nil {
			return err
		}
		receipt.Providers[address] = table.RestoredInheritedForeignIndex{Current: index, Eligible: eligible}
	}
	pending.Child = sourceRef
	pending.Definitions[0].Target = targetRef.Address
	pending.Definitions[0].Path = []ids.ObjectAddress{target.Namespace()}
	pending.Definitions[0].Inherited = receipt
	return nil
}

// RestoredInheritedForeignKeyUnchanged reports whether view still holds the
// relations, inheritance edge, key columns and target indexes receipt
// observed. Comments and final attribute-rank repairs do not alter these
// observations; no later key, owner, type or provider incarnation can reuse
// them.
func RestoredInheritedForeignKeyUnchanged(receipt *table.RestoredInheritedForeignKey, view catalog.Read) bool {
	source, _ := view.Relation(receipt.Source.Address)
	target, _ := view.Relation(receipt.Target.Address)
	if !table.InheritedForeignRelationUnchanged(source, receipt.BeforeSource) || !table.InheritedForeignRelationUnchanged(target, receipt.BeforeTarget) || !table.InheritedForeignKeyDescendant(view, target, source.Address()) || !table.InheritedForeignColumnsUnchanged(source, receipt.Columns) || !table.InheritedForeignColumnsUnchanged(source, receipt.Deleted) || !table.InheritedForeignColumnsUnchanged(target, receipt.Referenced) {
		return false
	}
	seen := 0
	for address, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != target.Address() {
			continue
		}
		observed, exists := receipt.Providers[address]
		if !exists || index.SQLIndex != observed.Current.SQLIndex || index.Owner != observed.Current.Owner || index.Namespace() != observed.Current.Namespace() || index.Name() != observed.Current.Name() || len(index.Columns) != len(observed.Current.Columns) {
			return false
		}
		for i, column := range index.Columns {
			if column.Usage != observed.Current.Columns[i].Usage {
				return false
			}
		}
		seen++
	}
	return seen == len(receipt.Providers)
}
