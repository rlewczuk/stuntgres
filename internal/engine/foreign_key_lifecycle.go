package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

// Column retirement already owns local AUTO definitions and shape projection.
// Consume only FK metadata edges from shared discovery; never reenter the
// whole-provider projection controller before the column's own pruning.
func foreignKeyColumnDependencies(provider dropDependencyReader, target ids.ObjectAddress, mode parser.DropMode) ([]domainDependency, error) {
	present := false
	for address, descriptor := range provider.InputDescriptors() {
		if replacement := provider.Replacement(address); replacement != nil {
			descriptor = replacement
		}
		if d, ok := descriptor.(*catalog.Relation); ok && len(d.Constraints.ForeignKeys) != 0 {
			present = true
			break
		}
	}
	if !present {
		return nil, nil
	}
	discovered, err := provider.dependencyClosure([]ids.ObjectAddress{target})
	if err != nil {
		return nil, err
	}
	foreign := discovered[:0]
	for _, edge := range discovered {
		if edge.address.ClassID != 2606 || edge.relation == nil {
			continue
		}
		for _, f := range edge.relation.Constraints.ForeignKeys {
			if f.OID == edge.address.ObjectID {
				// The column's existing shape pass owns local AUTO removal,
				// even if discovery first reached this FK through its index.
				if edge.relation.Address().ClassID == target.ClassID && edge.relation.Address().ObjectID == target.ObjectID && slices.Contains(f.Attributes, target.SubID) {
					break
				}
				edge.target = target
				foreign = append(foreign, edge)
				break
			}
		}
	}
	if provider.statementBound() && mode == parser.DropCascade {
		var owners []ids.ObjectRef
		for _, edge := range foreign {
			if edge.automatic {
				continue
			}
			ref := provider.baseRef(edge.relation.Address())
			if !slices.Contains(owners, ref) {
				owners = append(owners, ref)
			}
		}
		slices.SortFunc(owners, func(a, b ids.ObjectRef) int {
			if a.Address.ObjectID < b.Address.ObjectID {
				return -1
			}
			if a.Address.ObjectID > b.Address.ObjectID {
				return 1
			}
			return 0
		})
		for _, ref := range owners {
			if err := provider.acquire(ref, true); err != nil {
				return nil, err
			}
		}
	}
	return foreign, nil
}

// The ordinary dependency closure owns discovery/order/RESTRICT diagnostics.
// This pass consumes only FK definition edges, never the referencing table/data.
func foreignKeyDependents(provider dropDependencyReader, edges []domainDependency) error {
	for _, edge := range edges {
		if edge.address.ClassID != 2606 || edge.relation == nil {
			continue
		}
		if provider.Removed(edge.relation.Address()) {
			continue
		}
		old := edge.relation
		if replacement, ok := provider.Replacement(old.Address()).(*catalog.Relation); ok {
			old = replacement
		}
		for i, f := range old.Constraints.ForeignKeys {
			if f.OID != edge.address.ObjectID {
				continue
			}
			next := *old
			next.Constraints.ForeignKeys = make([]catalog.ForeignKeyConstraint, 0, len(old.Constraints.ForeignKeys)-1)
			next.Constraints.ForeignKeys = append(next.Constraints.ForeignKeys, old.Constraints.ForeignKeys[:i]...)
			next.Constraints.ForeignKeys = append(next.Constraints.ForeignKeys, old.Constraints.ForeignKeys[i+1:]...)
			var err error
			next.ObjectRevision, err = provider.revision(old.Revision())
			if err != nil {
				return err
			}
			if err := provider.Replace(&next); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

func (e *Engine) rebuildAlterForeignKeysLocked(stage *CatalogStage, parent *catalog.Relation, transformations []index.ColumnTransformation) (*catalog.Relation, error) {
	if len(transformations) == 0 {
		return parent, nil
	}
	view := e.stageRead(stage)
	for _, descriptor := range view.Descriptors() {
		old, ok := descriptor.(*catalog.Relation)
		if !ok || len(old.Constraints.ForeignKeys) == 0 {
			continue
		}
		var next *catalog.Relation
		for i, f := range old.Constraints.ForeignKeys {
			affected := false
			for _, change := range transformations {
				affected = affected || (old.Address() == parent.Address() && slices.Contains(f.Attributes, change.Attribute)) || (f.Target == parent.Address() && slices.Contains(f.Referenced, change.Attribute))
			}
			if !affected {
				continue
			}
			if next == nil {
				copy := *old
				copy.Constraints.ForeignKeys = slices.Clone(old.Constraints.ForeignKeys)
				next = &copy
				if old.Address() == parent.Address() {
					*next = *parent
					next.Constraints.ForeignKeys = slices.Clone(parent.Constraints.ForeignKeys)
				}
			}
			c := &next.Constraints.ForeignKeys[i]
			target, ok := view.Relation(c.Target)
			if c.Target == parent.Address() {
				target = parent
				ok = true
			}
			if !ok {
				return nil, ids.ErrCatalogReplaced
			}
			index, err := foreignKeyIndex(view, target, c.Referenced)
			if err != nil {
				return nil, err
			}
			c.Index = index.Address()
			if _, err := bindForeignKeyComparison(view, next, target, index, *c); err != nil {
				return nil, err
			}
			c.OID, err = e.catalog.allocateOIDLocked()
			if err != nil {
				return nil, err
			}
		}
		if next != nil {
			var err error
			next.ObjectRevision, err = e.catalog.revisionLocked()
			if err != nil {
				return nil, err
			}
			if err := stage.Replace(next); err != nil {
				return nil, err
			}
			if next.Address() == parent.Address() {
				parent = next
			}
		}
	}
	return parent, nil
}

func (s *StatementScope) acquireForeignKeyTypeProviders(view catalog.Read, address ids.ObjectAddress) error {
	var refs []ids.ObjectRef
	e := s.engine()
	for _, descriptor := range view.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if child.Address() != address && f.Target != address {
				continue
			}
			for _, a := range []ids.ObjectAddress{child.Address(), f.Target} {
				ref := e.catalog.Domain.Ref(view.Epoch(), a)
				if !slices.Contains(refs, ref) {
					refs = append(refs, ref)
				}
			}
		}
	}
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int {
		if a.Address.ObjectID < b.Address.ObjectID {
			return -1
		}
		if a.Address.ObjectID > b.Address.ObjectID {
			return 1
		}
		return 0
	})
	for _, ref := range refs {
		if err := s.foreignKeyGrant(ref, 0, lock.RowShare, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) validateAlterForeignKeys(view catalog.Read, address ids.ObjectAddress) error {
	auth, err := operationAuthorization(s.op)
	if err != nil {
		return err
	}
	auth.Catalog = view
	auth.Recapture(auth.Catalog)
	e := s.engine()
	for _, descriptor := range view.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if f.NotValid || child.Address() != address && f.Target != address {
				continue
			}
			parent := view.MustRelation(f.Target)
			index := view.MustRelation(f.Index)
			comparisons, err := bindForeignKeyComparison(view, child, parent, index, f)
			if err != nil {
				return err
			}
			children, err := s.captureForeignKeyRows(e.catalog.Domain.Ref(view.Epoch(), child.Address()), ids.ObjectAddress{})
			if err != nil {
				return err
			}
			parents, err := s.captureForeignKeyRows(e.catalog.Domain.Ref(view.Epoch(), parent.Address()), f.Index)
			if err != nil {
				return err
			}
			if err := validateForeignKeyData(s.op.Context(), auth, child, parent, f, comparisons, children, parents, s.input, s.output); err != nil {
				return err
			}
		}
	}
	return nil
}

// Nested CREATE SCHEMA definitions share the normal candidate. Newly created
// providers need no live grant; external parents retain the canonical DDL mode.
func (s *StatementScope) acquireForeignKeyDefinitionProviders(stage *CatalogStage) error {
	var refs []ids.ObjectRef
	for address, descriptor := range stage.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if installedForeignKey(stage, address, f) {
				continue
			}
			if f.Target == address {
				continue
			}
			if _, external := stage.Base.Find(f.Target); !external {
				continue
			}
			ref := stage.owner.catalog.Domain.Ref(stage.Base.Epoch(), f.Target)
			if !slices.Contains(refs, ref) {
				refs = append(refs, ref)
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int {
		if a.Address.ObjectID < b.Address.ObjectID {
			return -1
		}
		if a.Address.ObjectID > b.Address.ObjectID {
			return 1
		}
		return 0
	})
	for _, ref := range refs {
		if err := s.foreignKeyGrant(ref, 0, lock.ShareRowExclusive, nil); err != nil {
			return err
		}
	}
	auth, err := operationAuthorization(s.op)
	if err != nil {
		return err
	}
	e := stage.owner
	e.mu.Lock()
	auth.Catalog = e.privateCatalogLocked(s.owner.Catalog)
	auth.Recapture(auth.Catalog)
	e.mu.Unlock()
	for address, descriptor := range stage.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if installedForeignKey(stage, address, f) {
				continue
			}
			if f.Target == address {
				continue
			}
			if _, external := stage.Base.Find(f.Target); !external {
				continue
			}
			parent, ok := auth.Catalog.Relation(f.Target)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			index, ok := auth.Catalog.Relation(f.Index)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			eligible, err := foreignKeyIndexEligible(parent, index, f.Referenced)
			if err != nil {
				return err
			}
			if !eligible {
				return ids.ErrCatalogReplaced
			}
			if _, err := bindForeignKeyComparison(auth.Catalog, child, parent, index, f); err != nil {
				return err
			}
			if err := foreignKeyReferencesAllowed(auth.View, auth, parent, f.Referenced); err != nil {
				return err
			}
		}
	}
	return nil
}
