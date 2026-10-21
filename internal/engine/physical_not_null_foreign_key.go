package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

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
		return nil, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
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
		if !index.PhysicalNotNullOriginValid(auth.Catalog, old, attribute, proof) || proof.Cause.Origin != ref || proof.Cause.Kind != catalog.PhysicalNotNullImportCleanup && proof.Cause.Kind != catalog.PhysicalNotNullImportFlag {
			return nil, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
		}
	}
	for i := range old.Constraints.Unique {
		primary := &old.Constraints.Unique[i]
		if !primary.Primary {
			continue
		}
		support := catalog.CapturePhysicalPrimarySupport(old, primary)
		if !index.PhysicalPrimarySupportValid(auth.Catalog, old, primary, support) {
			return nil, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
		}
		return &restoredPhysicalForeignKeyProvider{op: op, authority: auth, ref: ref, current: old, primary: support}, nil
	}
	return nil, nil
}

// Reuse the incumbent captured-FK binder, including its one data validation and
// publication. The optional supporting slot pins only this genuine current PK;
// ordinary declarations continue to use the unchanged zero-slot selection.
func (e *Engine) capturePhysicalSelfForeignKey(op *session.Operation, raw *parser.AlterTable, provider *restoredPhysicalForeignKeyProvider, created map[ids.ObjectAddress]ids.ObjectRef) (table.RestoredForeignKeys, bool, error) {
	if len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddForeignKey || raw.Actions[0].ForeignKey == nil || raw.Actions[0].ForeignKey.NotValid {
		return table.RestoredForeignKeys{}, false, nil
	}
	reject := func() (table.RestoredForeignKeys, bool, error) {
		return table.RestoredForeignKeys{}, false, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
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
		return table.RestoredForeignKeys{}, false, err
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
			eligible, eligibilityErr := table.ForeignKeyIndexEligible(old, index, attributes)
			if eligibilityErr != nil {
				return table.RestoredForeignKeys{}, false, eligibilityErr
			}
			if eligible {
				return reject()
			}
		}
		provider.permuted = true
	}
	pending, err := e.captureRestoredForeignKeys(op, []parser.ForeignKeyConstraint{*foreign})
	if err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	pending.Child = provider.ref
	if err := e.resolveRestoredForeignKeyTargets(&pending); err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	if pending.Definitions[0].Target != provider.ref.Address {
		return reject()
	}
	pending.Definitions[0].Supporting = provider.primary.Index
	return pending, true, nil
}
