package engine

import (
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func physicalSelfForeignKeyDeferred(table *relationDescriptor, foreign foreignKeyConstraint) bool {
	if len(table.PhysicalNotNull) == 0 || foreign.NotValid || foreign.Target != table.address {
		return false
	}
	for i := range table.UniqueConstraints {
		primary := &table.UniqueConstraints[i]
		if primary.Primary && foreign.Index == primary.Index && physicalSameAttributes(foreign.Referenced, primary.Attributes) {
			return true
		}
	}
	return false
}

func emitPhysicalSelfForeignKeys(view CatalogRead, table *relationDescriptor, owner string, separate bool, emit func(string) error, setOwner func(string) error) error {
	if len(table.PhysicalNotNull) == 0 || len(table.ForeignKeys) == 0 {
		return nil
	}
	qualified := quoteIdentifier(view.Get(table.namespace).Name()) + "." + quoteIdentifier(table.name)
	for _, foreign := range table.ForeignKeys {
		if !physicalSelfForeignKeyDeferred(table, foreign) {
			continue
		}
		index := view.MustRelation(foreign.Index)
		if partitionReplaySeparateConstraint(view, index) != separate {
			continue
		}
		primary := physicalPrimaryForAttribute(table, foreign.Referenced[0])
		text, err := (pgRenderer{view: view}).foreignKeyConstraintSQLWithPrimary(table, foreign, slices.Equal(foreign.Referenced, primary.Attributes))
		if err != nil {
			return err
		}
		if err := setOwner(owner); err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ADD CONSTRAINT " + quoteIdentifier(foreign.Name) + " " + text + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

type restoredPhysicalForeignKeyProvider struct {
	op             *Operation
	authority      AuthorizationView
	ref            ids.ObjectRef
	current        *relationDescriptor
	primary        *physicalPrimarySupport
	ownerDirective bool
	permuted       bool
}

// Only the actual completed constructor/cleanup can issue this short-lived
// readiness receipt. It never creates a key or grants foreign-key privileges.
func (e *Engine) capturePhysicalForeignKeyProvider(op *Operation, name parser.ObjectName, created map[ids.ObjectAddress]ids.ObjectRef) (*restoredPhysicalForeignKeyProvider, error) {
	auth, err := op.Authorization()
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) {
		return nil, sqlError("0A000", restorePhysicalNotNullBoundary)
	}
	ref, err := e.resolveRelationReference(auth, name, false)
	if err != nil {
		return nil, err
	}
	old, ok := auth.catalog.Relation(ref.Address)
	if !ok || created[ref.Address] != ref || len(old.PhysicalNotNull) == 0 || old.Partition != nil || old.Membership != nil {
		return nil, nil
	}
	for attribute, proof := range old.PhysicalNotNull {
		if !physicalNotNullOriginValid(auth.catalog, old, attribute, proof) || proof.cause.origin != ref || proof.cause.kind != physicalNotNullImportCleanup && proof.cause.kind != physicalNotNullImportFlag {
			return nil, sqlError("0A000", restorePhysicalNotNullBoundary)
		}
	}
	for i := range old.UniqueConstraints {
		primary := &old.UniqueConstraints[i]
		if !primary.Primary {
			continue
		}
		support := capturePhysicalPrimarySupport(old, primary)
		if !physicalPrimarySupportValid(auth.catalog, old, primary, support) {
			return nil, sqlError("0A000", restorePhysicalNotNullBoundary)
		}
		return &restoredPhysicalForeignKeyProvider{op: op, authority: auth, ref: ref, current: old, primary: support}, nil
	}
	return nil, nil
}

// Reuse the incumbent captured-FK binder, including its one data validation and
// publication. The optional supporting slot pins only this genuine current PK;
// ordinary declarations continue to use the unchanged zero-slot selection.
func (e *Engine) capturePhysicalSelfForeignKey(op *Operation, raw *parser.AlterTable, provider *restoredPhysicalForeignKeyProvider, created map[ids.ObjectAddress]ids.ObjectRef) (restoredForeignKeys, bool, error) {
	if len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddForeignKey || raw.Actions[0].ForeignKey == nil || raw.Actions[0].ForeignKey.NotValid {
		return restoredForeignKeys{}, false, nil
	}
	reject := func() (restoredForeignKeys, bool, error) {
		return restoredForeignKeys{}, false, sqlError("0A000", restorePhysicalNotNullBoundary)
	}
	foreign := raw.Actions[0].ForeignKey
	if provider == nil || provider.op != op || !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(foreign.Target.Parts) != 2 || foreign.Name.Text == "" || len(foreign.Columns) != len(provider.primary.keys) {
		return reject()
	}
	for i := range raw.Name.Parts {
		if raw.Name.Parts[i].Text != foreign.Target.Parts[i].Text {
			return reject()
		}
	}
	auth, err := op.Authorization()
	if err != nil {
		return restoredForeignKeys{}, false, err
	}
	e.mu.Lock()
	valid := partitionConstraintReplayImport(e, op, auth) && auth.roleRecord == provider.authority.roleRecord && created[provider.ref.Address] == provider.ref && provider.ref.Epoch == auth.catalog.Epoch() && auth.catalog.Get(provider.ref.Address) == provider.current
	e.mu.Unlock()
	if !valid {
		return reject()
	}
	old := provider.current
	namespace := auth.catalog.Get(old.namespace)
	if namespace == nil || namespace.Name() != raw.Name.Parts[0].Text || old.name != raw.Name.Parts[1].Text {
		return reject()
	}
	if len(foreign.Referenced) != 0 {
		if len(foreign.Referenced) != len(provider.primary.keys) {
			return reject()
		}
		permuted := false
		for i, column := range foreign.Referenced {
			slot, exists := old.Index[column.Text]
			if !exists {
				return reject()
			}
			attribute := old.Attributes[slot]
			if !slices.ContainsFunc(provider.primary.keys, func(key physicalPrimaryKeyUsage) bool { return key.attribute == attribute }) {
				return reject()
			}
			for _, prior := range foreign.Referenced[:i] {
				if prior.Text == column.Text {
					return reject()
				}
			}
			permuted = permuted || attribute != provider.primary.keys[i].attribute
		}
		if !permuted {
			return reject()
		}
		var keyBuffer [32]int32
		attributes := keyBuffer[:len(provider.primary.keys)]
		for i, key := range provider.primary.keys {
			attributes[i] = key.attribute
		}
		for _, descriptor := range auth.catalog.Descriptors() {
			index, ok := descriptor.(*relationDescriptor)
			if !ok || index.address == provider.primary.index {
				continue
			}
			eligible, eligibilityErr := foreignKeyIndexEligible(old, index, attributes)
			if eligibilityErr != nil {
				return restoredForeignKeys{}, false, eligibilityErr
			}
			if eligible {
				return reject()
			}
		}
		provider.permuted = true
	}
	pending, err := e.captureRestoredForeignKeys(op, []parser.ForeignKeyConstraint{*foreign})
	if err != nil {
		return restoredForeignKeys{}, false, err
	}
	pending.child = provider.ref
	if err := e.resolveRestoredForeignKeyTargets(&pending); err != nil {
		return restoredForeignKeys{}, false, err
	}
	if pending.definitions[0].target != provider.ref.Address {
		return reject()
	}
	pending.definitions[0].supporting = provider.primary.index
	return pending, true, nil
}
