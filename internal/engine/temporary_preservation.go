package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// Preserve session-owned temporary namespaces and generations during persistent
// replacement. Imported identity collisions are remapped, never temp identities.
func (e *Engine) preserveTemporaryLocked(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator, restoreOp *session.Operation, indexes *index.Snapshot) error {
	e.retainDatabaseLocked(next)
	current := e.catalog.read(e.catalog.state)
	if err := e.checkTemporaryRelationSourcesReplacementLocked(); err != nil {
		return err
	}
	if err := control.CheckTemporaryRuleSourcesLocked(current); err != nil {
		return err
	}
	temporary, err := control.TemporarySet(current, next)
	if err != nil {
		return err
	}
	// Restore the exact live builtin codecs in this private candidate before
	// checking the full TEMP policy runtime manifest. No allocator/publication
	// work occurs before dependency compatibility is established.
	typesChanged := control.RetainTemporaryBuiltinTypes(current, next, temporary)
	if err := control.CheckTemporaryDependencies(current, next, temporary); err != nil {
		return err
	}
	if err := control.RetainTemporaryViewProvidersLocked(current, next, temporary); err != nil {
		return err
	}
	mapping, err := control.TemporaryCollisionMapping(next, temporary, e.catalog.allocateOIDLocked)
	if err != nil {
		return err
	}
	if err := typedef.ImportScalarTypesLocked(next, mapping, e.catalog.allocateEnumMemberLocked); err != nil {
		return err
	}
	defer func() { next.SetScalarImports(nil) }()
	domainsChanged, err := typedef.ReownDomainTypes(next, mapping, true)
	if err != nil {
		return err
	}
	typedef.RefreshScalarImportDestinations(next)
	indexes.CaptureMapping(mapping, next.ScalarImports())
	if typesChanged || domainsChanged || len(mapping) != 0 || len(next.ScalarImports()) != 0 || next.ACLItemImport() != nil {
		if err := stored.ReownScalarTypes(next, relations, mapping); err != nil {
			return err
		}
	}
	if err := control.RemapReplacementIdentities(current, next, temporary, mapping, relations, sequences); err != nil {
		return err
	}
	next.SetEpoch(e.catalog.state.Epoch() + 1)
	if err := routine.RebindSQLDefinitions(e.catalog.read(next), next, mapping); err != nil {
		return err
	}
	if err := e.remapCatalogExpressions(next, mapping); err != nil {
		return err
	}
	if err := index.RebindImportedExclusions(next); err != nil {
		return err
	}
	installedStage, err := e.repairRemappedGenerationsLocked(ctx, next, relations, sequences, mapping, restoreOp, indexes)
	if err != nil {
		return err
	}
	operators := catalog.OperatorIndexEdit{Index: next.OperatorNames()}
	procedures := catalog.ProcedureIndexEdit{Index: next.ProcedureNames()}
	for a, d := range temporary {
		operators.Replace(next.Get(a), d)
		procedures.Replace(next.Get(a), d)
		next.SetDescriptor(a, d)
		next.BindName(catalog.DescriptorNameKey(d), a)
		// Repair may have published these candidate containers into its engine.
		// The target mutex is already held. A distinct stage is isolated and
		// has finished provider evaluation; no callback runs under both locks.
		if installedStage != nil && installedStage != e {
			installedStage.mu.Lock()
		}
		if rows := e.storage.Relations[a]; rows != nil {
			if installedStage == nil {
				relations[a] = rows
			} else {
				installedStage.storage.InstallRelationLocked(a, rows)
			}
		}
		if g := e.storage.Sequences[a]; g != nil {
			if installedStage == nil {
				sequences[a] = g
			} else {
				installedStage.storage.InstallSequenceLocked(a, g)
			}
		}
		if installedStage != nil && installedStage != e {
			installedStage.mu.Unlock()
		}
	}
	next.SetOperatorNames(operators.Index)
	next.SetProcedureNames(procedures.Index)
	for address, descriptor := range temporary {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || len(relation.Security.Policies) == 0 && len(stored.RulesOf(relation)) == 0 {
			continue
		}
		view := e.catalog.read(next)
		remap := func(ref ids.ObjectRef) ids.ObjectRef { ref.Epoch = next.Epoch(); return ref }
		typ := func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }
		// Replacement preflight certified these exact TEMP providers. They
		// enter the candidate after imported expressions have been remapped,
		// so advance their frozen definition refs without applying import IDs.
		if len(relation.Security.Policies) != 0 {
			replacement, err := stored.RemapPolicies(view, relation, nil, remap, typ, nil)
			if err != nil {
				return err
			}
			relation = replacement
		}
		if len(stored.RulesOf(relation)) != 0 {
			replacement, err := stored.RemapRules(view, relation, nil, remap, typ, nil)
			if err != nil {
				return err
			}
			relation = replacement
		}
		next.SetDescriptor(address, relation)
	}
	previous := make(map[ids.ObjectAddress]catalog.Descriptor, next.DescriptorCount())
	for address, descriptor := range next.Descriptors() {
		previous[address] = descriptor
	}
	if err := control.RemapViewRuleOIDs(e.catalog.read(next), e.catalog.allocateOIDLocked); err != nil {
		return err
	}
	if err := table.RemapInheritanceConstraintOrigins(next, previous); err != nil {
		return err
	}
	if err := table.RemapRetiredConstraints(e.catalog.Domain, next, previous); err != nil {
		return err
	}
	if err := table.RemapPhysicalNotNull(e.catalog.read(next), previous); err != nil {
		return err
	}
	for _, descriptor := range next.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok && len(relation.Security.Policies) != 0 {
			if err := view.ValidatePolicies(e.catalog.read(next), relation); err != nil {
				return err
			}
		}
		if relation, ok := descriptor.(*catalog.Relation); ok {
			if err := typedef.ValidateTypedAttachment(e.catalog.read(next), relation); err != nil {
				return err
			}
		}
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.Inheritance != nil {
			if err := table.ValidateInheritance(e.catalog.read(next), relation); err != nil {
				return err
			}
		}
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.View != nil {
			if err := view.ValidateViewDefinition(e.catalog.read(next), relation); err != nil {
				return err
			}
		}
	}
	return nil
}

// Called under Engine.mu at preallocation and final replacement admission.
// Retained TEMP relations cannot substitute persistent inheritance or OF
// sources: control checks the inheritance parents, then the typed check runs.
func (e *Engine) checkTemporaryRelationSourcesReplacementLocked() error {
	if err := control.CheckTemporaryRelationSourcesLocked(e.catalog.state); err != nil {
		return err
	}
	return typedef.CheckTemporaryTypedReplacementLocked(e.catalog.state)
}
