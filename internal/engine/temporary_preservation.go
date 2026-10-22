package engine

import (
	"context"
	"fmt"
	"slices"
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
	if err := e.checkTemporaryRelationSourcesReplacementLocked(); err != nil {
		return err
	}
	if err := e.checkTemporaryRuleSourcesReplacementLocked(); err != nil {
		return err
	}
	current := e.catalog.read(e.catalog.state)
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
	if err := e.retainTemporaryViewProvidersLocked(next, temporary); err != nil {
		return err
	}
	mapping, err := control.TemporaryCollisionMapping(next, temporary, e.catalog.allocateOIDLocked)
	if err != nil {
		return err
	}
	if err := e.importScalarTypesLocked(next, mapping); err != nil {
		return err
	}
	defer func() { next.SetScalarImports(nil) }()
	domainsChanged, err := typedef.ReownDomainTypes(next, mapping, true)
	if err != nil {
		return err
	}
	refreshScalarImportDestinations(next)
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

// Only immutable bootstrap catalog sources have a persistent survivor route.
// Retain the predecessor itself, after checking the candidate's full provider
// shape and namespace/ACL authority; a compatible-looking user table is not it.
func (e *Engine) retainTemporaryViewProvidersLocked(next *catalog.State, temporary map[ids.ObjectAddress]catalog.Descriptor) error {
	view := e.catalog.read(e.catalog.state)
	for _, descriptor := range temporary {
		consumer, ok := descriptor.(*catalog.Relation)
		if !ok || consumer.View == nil {
			continue
		}
		for ref, captured := range stored.ViewOf(consumer).References() {
			address := ref.Address
			address.SubID = 0
			old, ok := e.catalog.state.Relation(address)
			if !ok {
				continue
			}
			if temporary[old.Namespace()] != nil {
				if !stored.ViewReferenceValid(view, consumer, ref) {
					return ids.ErrCatalogReplaced
				}
				continue
			}
			fail := func() error {
				return fmt.Errorf("stuntgres: catalog replacement with live temporary view persistent dependencies is unsupported")
			}
			if old != captured || old.RelationKind != catalog.CatalogRelation || !stored.DependPinned(view, nil, address) {
				return fail()
			}
			candidate, ok := next.Relation(address)
			if !ok || candidate.RelationKind != old.RelationKind || candidate.Namespace() != old.Namespace() || candidate.Name() != old.Name() || candidate.Owner != old.Owner || candidate.VirtualView != old.VirtualView || candidate.History.HasIndexes != old.History.HasIndexes || candidate.History.HasSubclasses != old.History.HasSubclasses || candidate.ColumnMeta.HighWater != old.ColumnMeta.HighWater || !slices.Equal(candidate.Attributes, old.Attributes) || len(candidate.Columns) != len(old.Columns) || candidate.Security.ACL.Explicit != old.Security.ACL.Explicit || !slices.Equal(candidate.Security.ACL.Entries, old.Security.ACL.Entries) || len(candidate.Security.ColumnACL) != len(old.Security.ColumnACL) {
				return fail()
			}
			for attribute, acl := range old.Security.ColumnACL {
				other, ok := candidate.Security.ColumnACL[attribute]
				if !ok || other.Explicit != acl.Explicit || !slices.Equal(other.Entries, acl.Entries) {
					return fail()
				}
			}
			for i, column := range old.Columns {
				other := candidate.Columns[i]
				if column.Name != other.Name || column.Usage.Type.ID() != other.Usage.Type.ID() || column.Usage.Typmod != other.Usage.Typmod || column.Usage.Collation.ID != other.Usage.Collation.ID || column.Nullable != other.Nullable || column.NullabilityKnown != other.NullabilityKnown {
					return fail()
				}
			}
			namespace, ok := e.catalog.state.Namespace(old.Namespace())
			other, candidateOK := next.Namespace(old.Namespace())
			if !ok || !candidateOK || !stored.DependPinned(view, nil, old.Namespace()) || namespace.Name() != other.Name() || namespace.Owner != other.Owner || namespace.Temporary != other.Temporary || namespace.TemporaryToast != other.TemporaryToast || namespace.ACL.Explicit != other.ACL.Explicit || !slices.Equal(namespace.ACL.Entries, other.ACL.Entries) {
				return fail()
			}
			next.SetDescriptor(address, old)
			next.SetDescriptor(old.Namespace(), namespace)
		}
	}
	return nil
}

const temporaryInheritanceReplacementError = "stuntgres: catalog replacement with live temporary table inheritance of persistent parents is unsupported"

// Called under Engine.mu at preallocation and final replacement admission.
// Retained TEMP relations cannot substitute persistent inheritance or OF sources.
func (e *Engine) checkTemporaryRelationSourcesReplacementLocked() error {
	for _, descriptor := range e.catalog.state.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || table.Inheritance == nil {
			continue
		}
		namespace := e.catalog.state.MustNamespace(table.Namespace())
		if namespace.Temporary == (ids.SessionToken{}) {
			continue
		}
		for _, edge := range table.Inheritance.Parents {
			parent := edge.Relation
			source, ok := e.catalog.state.Relation(parent)
			if !ok {
				return fmt.Errorf("stuntgres: missing inheritance parent")
			}
			if e.catalog.state.MustNamespace(source.Namespace()).Temporary == (ids.SessionToken{}) {
				return fmt.Errorf("%s", temporaryInheritanceReplacementError)
			}
		}
	}
	return e.checkTemporaryTypedReplacementLocked()
}

func (e *Engine) checkTemporaryRuleSourcesReplacementLocked() error {
	view := e.catalog.read(e.catalog.state)
	for _, descriptor := range e.catalog.state.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || len(stored.RulesOf(d)) == 0 {
			continue
		}
		ns, ok := e.catalog.state.Namespace(d.Namespace())
		if !ok || ns.Temporary == (ids.SessionToken{}) {
			continue
		}
		for _, rule := range stored.RulesOf(d) {
			for _, address := range rule.References() {
				address.SubID = 0
				if stored.DependPinned(view, nil, address) {
					continue
				}
				provider := e.catalog.state.Get(address)
				if address.ClassID == 2606 {
					parent := stored.ConstraintParent(view, address)
					if parent == nil {
						return ids.ErrCatalogReplaced
					}
					provider = parent
				}
				if provider == nil {
					return ids.ErrCatalogReplaced
				}
				providerNS, ok := e.catalog.state.Namespace(provider.Namespace())
				if !ok || providerNS.Temporary != ns.Temporary {
					return fmt.Errorf("stuntgres: catalog replacement cannot preserve temporary rule %q dependencies", rule.Name)
				}
			}
		}
	}
	return nil
}
