package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func physicalSelfForeignKeyDeferred(table *catalog.Relation, foreign catalog.ForeignKeyConstraint) bool {
	if len(table.Constraints.PhysicalNotNull) == 0 || foreign.NotValid || foreign.Target != table.Address() {
		return false
	}
	for i := range table.Constraints.Unique {
		primary := &table.Constraints.Unique[i]
		if primary.Primary && foreign.Index == primary.Index && physicalSameAttributes(foreign.Referenced, primary.Attributes) {
			return true
		}
	}
	return false
}

func emitPhysicalSelfForeignKeys(view catalog.Read, table *catalog.Relation, owner string, separate bool, emit func(string) error, setOwner func(string) error) error {
	if len(table.Constraints.PhysicalNotNull) == 0 || len(table.Constraints.ForeignKeys) == 0 {
		return nil
	}
	qualified := catalog.QuoteIdentifier(view.Get(table.Namespace()).Name()) + "." + catalog.QuoteIdentifier(table.Name())
	for _, foreign := range table.Constraints.ForeignKeys {
		if !physicalSelfForeignKeyDeferred(table, foreign) {
			continue
		}
		index := view.MustRelation(foreign.Index)
		if PartitionReplaySeparateConstraint(view, index) != separate {
			continue
		}
		primary := catalog.PhysicalPrimaryForAttribute(table, foreign.Referenced[0])
		text, err := (pgRenderer{View: view}).ForeignKeyConstraintSQLWithPrimary(table, foreign, slices.Equal(foreign.Referenced, primary.Attributes))
		if err != nil {
			return err
		}
		if err := setOwner(owner); err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ADD CONSTRAINT " + catalog.QuoteIdentifier(foreign.Name) + " " + text + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

type restoredPhysicalForeignKeyProvider struct {
	op             *session.Operation
	authority      AuthorizationView
	ref            ids.ObjectRef
	current        *catalog.Relation
	primary        *catalog.PhysicalPrimarySupport
	ownerDirective bool
	permuted       bool
}

// Only the actual completed constructor/cleanup can issue this short-lived
// readiness receipt. It never creates a key or grants foreign-key privileges.
func (e *Engine) capturePhysicalForeignKeyProvider(op *session.Operation, name parser.ObjectName, created map[ids.ObjectAddress]ids.ObjectRef) (*restoredPhysicalForeignKeyProvider, error) {
	auth, err := operationAuthorization(op)
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
	old, ok := auth.Catalog.Relation(ref.Address)
	if !ok || created[ref.Address] != ref || len(old.Constraints.PhysicalNotNull) == 0 || old.Partition != nil || old.Membership != nil {
		return nil, nil
	}
	for attribute, proof := range old.Constraints.PhysicalNotNull {
		if !PhysicalNotNullOriginValid(auth.Catalog, old, attribute, proof) || proof.Cause.Origin != ref || proof.Cause.Kind != catalog.PhysicalNotNullImportCleanup && proof.Cause.Kind != catalog.PhysicalNotNullImportFlag {
			return nil, sqlError("0A000", restorePhysicalNotNullBoundary)
		}
	}
	for i := range old.Constraints.Unique {
		primary := &old.Constraints.Unique[i]
		if !primary.Primary {
			continue
		}
		support := catalog.CapturePhysicalPrimarySupport(old, primary)
		if !PhysicalPrimarySupportValid(auth.Catalog, old, primary, support) {
			return nil, sqlError("0A000", restorePhysicalNotNullBoundary)
		}
		return &restoredPhysicalForeignKeyProvider{op: op, authority: auth, ref: ref, current: old, primary: support}, nil
	}
	return nil, nil
}

// Reuse the incumbent captured-FK binder, including its one data validation and
// publication. The optional supporting slot pins only this genuine current PK;
// ordinary declarations continue to use the unchanged zero-slot selection.
func (e *Engine) capturePhysicalSelfForeignKey(op *session.Operation, raw *parser.AlterTable, provider *restoredPhysicalForeignKeyProvider, created map[ids.ObjectAddress]ids.ObjectRef) (restoredForeignKeys, bool, error) {
	if len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddForeignKey || raw.Actions[0].ForeignKey == nil || raw.Actions[0].ForeignKey.NotValid {
		return restoredForeignKeys{}, false, nil
	}
	reject := func() (restoredForeignKeys, bool, error) {
		return restoredForeignKeys{}, false, sqlError("0A000", restorePhysicalNotNullBoundary)
	}
	foreign := raw.Actions[0].ForeignKey
	if provider == nil || provider.op != op || !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(foreign.Target.Parts) != 2 || foreign.Name.Text == "" || len(foreign.Columns) != len(provider.primary.Keys) {
		return reject()
	}
	for i := range raw.Name.Parts {
		if raw.Name.Parts[i].Text != foreign.Target.Parts[i].Text {
			return reject()
		}
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return restoredForeignKeys{}, false, err
	}
	e.mu.Lock()
	valid := partitionConstraintReplayImport(e, op, auth) && auth.RoleRecord == provider.authority.RoleRecord && created[provider.ref.Address] == provider.ref && provider.ref.Epoch == auth.Catalog.Epoch() && auth.Catalog.Get(provider.ref.Address) == provider.current
	e.mu.Unlock()
	if !valid {
		return reject()
	}
	old := provider.current
	namespace := auth.Catalog.Get(old.Namespace())
	if namespace == nil || namespace.Name() != raw.Name.Parts[0].Text || old.Name() != raw.Name.Parts[1].Text {
		return reject()
	}
	if len(foreign.Referenced) != 0 {
		if len(foreign.Referenced) != len(provider.primary.Keys) {
			return reject()
		}
		permuted := false
		for i, column := range foreign.Referenced {
			slot, exists := old.Index[column.Text]
			if !exists {
				return reject()
			}
			attribute := old.Attributes[slot]
			if !slices.ContainsFunc(provider.primary.Keys, func(key catalog.PhysicalPrimaryKeyUsage) bool { return key.Attribute == attribute }) {
				return reject()
			}
			for _, prior := range foreign.Referenced[:i] {
				if prior.Text == column.Text {
					return reject()
				}
			}
			permuted = permuted || attribute != provider.primary.Keys[i].Attribute
		}
		if !permuted {
			return reject()
		}
		var keyBuffer [32]int32
		attributes := keyBuffer[:len(provider.primary.Keys)]
		for i, key := range provider.primary.Keys {
			attributes[i] = key.Attribute
		}
		for _, descriptor := range auth.Catalog.Descriptors() {
			index, ok := descriptor.(*catalog.Relation)
			if !ok || index.Address() == provider.primary.Index {
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
	pending.definitions[0].supporting = provider.primary.Index
	return pending, true, nil
}
