package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

func (r pgRenderer) foreignKeyConstraintSQL(child *catalog.Relation, f catalog.ForeignKeyConstraint) (string, error) {
	return r.foreignKeyConstraintSQLWithPrimary(child, f, false)
}

// Implicit primary references preserve the captured provider when its deferred
// construction follows another UNIQUE over the same attributes.
func (r pgRenderer) foreignKeyConstraintSQLWithPrimary(child *catalog.Relation, f catalog.ForeignKeyConstraint, primary bool) (string, error) {
	view, path := r.view, r.path
	parent, ok := view.Relation(f.Target)
	if !ok {
		return "", fmt.Errorf("stuntgres: missing foreign-key deparse target")
	}
	local := make([]string, len(f.Attributes))
	var referenced []string
	if !primary {
		referenced = make([]string, len(f.Referenced))
	}
	for i, a := range f.Attributes {
		var err error
		local[i], err = r.attribute(child, a)
		if err != nil {
			return "", err
		}
		if !primary {
			referenced[i], err = r.attribute(parent, f.Referenced[i])
			if err != nil {
				return "", err
			}
		}
	}
	name := r.identifier(parent.Name())
	visible := false
	for _, namespace := range path {
		if a, ok := view.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: parent.Name()}); ok {
			visible = a == parent.Address()
			break
		}
	}
	if !visible {
		name = r.identifier(view.Get(parent.Namespace()).Name()) + "." + name
	}
	text := "FOREIGN KEY (" + strings.Join(local, ", ") + ") REFERENCES " + name
	if !primary {
		text += "(" + strings.Join(referenced, ", ") + ")"
	}
	if f.Full {
		text += " MATCH FULL"
	}
	for _, direction := range []struct {
		name   string
		action parser.ForeignKeyAction
	}{{"UPDATE", f.UpdateAction}, {"DELETE", f.DeleteAction}} {
		if direction.action == parser.ForeignKeyNoAction {
			continue
		}
		action, err := foreignKeyActionSQL(direction.action)
		if err != nil {
			return "", err
		}
		text += " ON " + direction.name + " " + action
		if direction.name == "DELETE" && len(f.DeleteAttributes) != 0 {
			labels := make([]string, len(f.DeleteAttributes))
			for i, a := range f.DeleteAttributes {
				labels[i], err = deparseAttribute(child, a)
				if err != nil {
					return "", err
				}
			}
			text += " (" + strings.Join(labels, ", ") + ")"
		}
	}
	if f.NotValid {
		text += " NOT VALID"
	}
	return text, nil
}

func foreignKeyActionSQL(action parser.ForeignKeyAction) (string, error) {
	switch action {
	case parser.ForeignKeyNoAction:
		return "NO ACTION", nil
	case parser.ForeignKeyRestrict:
		return "RESTRICT", nil
	case parser.ForeignKeyCascade:
		return "CASCADE", nil
	case parser.ForeignKeySetNull:
		return "SET NULL", nil
	case parser.ForeignKeySetDefault:
		return "SET DEFAULT", nil
	default:
		return "", fmt.Errorf("stuntgres: invalid foreign-key action")
	}
}

// Column retirement already owns local AUTO definitions and shape projection.
// Consume only FK metadata edges from shared discovery; never reenter the
// whole-provider projection controller before the column's own pruning.
func foreignKeyColumnDependencies(ctx context.Context, stage *CatalogStage, target ids.ObjectAddress, mode parser.DropMode, scope *StatementScope) ([]domainDependency, error) {
	return stageDropProvider(ctx, stage, scope, nil, nil).foreignKeyColumnDependencies(target, mode)
}

func (provider *dropProvider) foreignKeyColumnDependencies(target ids.ObjectAddress, mode parser.DropMode) ([]domainDependency, error) {
	scope := provider.scope
	present := false
	for address, descriptor := range provider.inputDescriptors() {
		if replacement := provider.replacement(address); replacement != nil {
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
	if scope != nil && mode == parser.DropCascade {
		var owners []ids.ObjectRef
		for _, edge := range foreign {
			if edge.automatic {
				continue
			}
			ref := provider.owner.catalog.Domain.Ref(provider.base.Epoch(), edge.relation.Address())
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
func stageForeignKeyDependents(stage *CatalogStage, edges []domainDependency) error {
	e := stage.owner
	e.mu.Lock()
	defer e.mu.Unlock()
	return stageForeignKeyDependentsLocked(stage, edges)
}

func stageForeignKeyDependentsLocked(stage *CatalogStage, edges []domainDependency) error {
	return stageDropProvider(context.Background(), stage, nil, nil, nil).foreignKeyDependents(edges)
}

func (provider *dropProvider) foreignKeyDependents(edges []domainDependency) error {
	for _, edge := range edges {
		if edge.address.ClassID != 2606 || edge.relation == nil {
			continue
		}
		if provider.removed(edge.relation.Address()) {
			continue
		}
		old := edge.relation
		if replacement, ok := provider.replacement(old.Address()).(*catalog.Relation); ok {
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
			if err := provider.replace(&next); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

func (e *Engine) rebuildAlterForeignKeysLocked(stage *CatalogStage, parent *catalog.Relation, transformations []columnTransformation) (*catalog.Relation, error) {
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
				affected = affected || (old.Address() == parent.Address() && slices.Contains(f.Attributes, change.attribute)) || (f.Target == parent.Address() && slices.Contains(f.Referenced, change.attribute))
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
	e := s.op.session.Engine()
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
	auth, err := s.op.Authorization()
	if err != nil {
		return err
	}
	auth.catalog = view
	auth.recaptureCatalog(auth.catalog)
	e := s.op.session.Engine()
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
	for address, descriptor := range stage.Descriptors {
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
	auth, err := s.op.Authorization()
	if err != nil {
		return err
	}
	e := stage.owner
	e.mu.Lock()
	auth.catalog = e.privateCatalogLocked(s.owner.catalog)
	auth.recaptureCatalog(auth.catalog)
	e.mu.Unlock()
	for address, descriptor := range stage.Descriptors {
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
			parent, ok := auth.catalog.Relation(f.Target)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			index, ok := auth.catalog.Relation(f.Index)
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
			if _, err := bindForeignKeyComparison(auth.catalog, child, parent, index, f); err != nil {
				return err
			}
			if err := foreignKeyReferencesAllowed(auth, parent, f.Referenced); err != nil {
				return err
			}
		}
	}
	return nil
}
