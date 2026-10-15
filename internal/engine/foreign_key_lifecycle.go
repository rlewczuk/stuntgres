package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/parser"
)

func (r pgRenderer) foreignKeyConstraintSQL(child *relationDescriptor, f foreignKeyConstraint) (string, error) {
	return r.foreignKeyConstraintSQLWithPrimary(child, f, false)
}

// Implicit primary references preserve the captured provider when its deferred
// construction follows another UNIQUE over the same attributes.
func (r pgRenderer) foreignKeyConstraintSQLWithPrimary(child *relationDescriptor, f foreignKeyConstraint, primary bool) (string, error) {
	view, path := r.view, r.path
	parent, ok := view.state.descriptors[f.Target].(*relationDescriptor)
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
	name := r.identifier(parent.name)
	visible := false
	for _, namespace := range path {
		if a, ok := view.state.names[NameKey{ClassID: RelationClassID, Namespace: namespace, Name: parent.name}]; ok {
			visible = a == parent.address
			break
		}
	}
	if !visible {
		name = r.identifier(view.state.descriptors[parent.namespace].Name()) + "." + name
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
func foreignKeyColumnDependencies(ctx context.Context, stage *CatalogStage, target ObjectAddress, mode parser.DropMode, scope *StatementScope) ([]domainDependency, error) {
	return stageDropProvider(ctx, stage, scope, nil, nil).foreignKeyColumnDependencies(target, mode)
}

func (provider *dropProvider) foreignKeyColumnDependencies(target ObjectAddress, mode parser.DropMode) ([]domainDependency, error) {
	scope := provider.scope
	present := false
	for address, descriptor := range provider.inputDescriptors() {
		if replacement := provider.replacement(address); replacement != nil {
			descriptor = replacement
		}
		if d, ok := descriptor.(*relationDescriptor); ok && len(d.foreignKeys) != 0 {
			present = true
			break
		}
	}
	if !present {
		return nil, nil
	}
	discovered, err := provider.dependencyClosure([]ObjectAddress{target})
	if err != nil {
		return nil, err
	}
	foreign := discovered[:0]
	for _, edge := range discovered {
		if edge.address.ClassID != 2606 || edge.relation == nil {
			continue
		}
		for _, f := range edge.relation.foreignKeys {
			if f.OID == edge.address.ObjectID {
				// The column's existing shape pass owns local AUTO removal,
				// even if discovery first reached this FK through its index.
				if edge.relation.address.ClassID == target.ClassID && edge.relation.address.ObjectID == target.ObjectID && slices.Contains(f.Attributes, target.SubID) {
					break
				}
				edge.target = target
				foreign = append(foreign, edge)
				break
			}
		}
	}
	if scope != nil && mode == parser.DropCascade {
		var owners []ObjectRef
		for _, edge := range foreign {
			if edge.automatic {
				continue
			}
			ref := ObjectRef{owner: provider.owner.owner, Epoch: provider.base.Epoch(), Address: edge.relation.address}
			if !slices.Contains(owners, ref) {
				owners = append(owners, ref)
			}
		}
		slices.SortFunc(owners, func(a, b ObjectRef) int {
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
		if provider.removed(edge.relation.address) {
			continue
		}
		old := edge.relation
		if replacement, ok := provider.replacement(old.address).(*relationDescriptor); ok {
			old = replacement
		}
		for i, f := range old.foreignKeys {
			if f.OID != edge.address.ObjectID {
				continue
			}
			next := *old
			next.foreignKeys = make([]foreignKeyConstraint, 0, len(old.foreignKeys)-1)
			next.foreignKeys = append(next.foreignKeys, old.foreignKeys[:i]...)
			next.foreignKeys = append(next.foreignKeys, old.foreignKeys[i+1:]...)
			var err error
			next.revision, err = provider.revision(old.revision)
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

func (e *Engine) rebuildAlterForeignKeysLocked(stage *CatalogStage, parent *relationDescriptor, transformations []columnTransformation) (*relationDescriptor, error) {
	if len(transformations) == 0 {
		return parent, nil
	}
	view := e.stageRead(stage)
	for _, descriptor := range view.state.descriptors {
		old, ok := descriptor.(*relationDescriptor)
		if !ok || len(old.foreignKeys) == 0 {
			continue
		}
		var next *relationDescriptor
		for i, f := range old.foreignKeys {
			affected := false
			for _, change := range transformations {
				affected = affected || (old.address == parent.address && slices.Contains(f.Attributes, change.attribute)) || (f.Target == parent.address && slices.Contains(f.Referenced, change.attribute))
			}
			if !affected {
				continue
			}
			if next == nil {
				copy := *old
				copy.foreignKeys = slices.Clone(old.foreignKeys)
				next = &copy
				if old.address == parent.address {
					*next = *parent
					next.foreignKeys = slices.Clone(parent.foreignKeys)
				}
			}
			c := &next.foreignKeys[i]
			target, ok := view.state.descriptors[c.Target].(*relationDescriptor)
			if c.Target == parent.address {
				target = parent
				ok = true
			}
			if !ok {
				return nil, ErrCatalogReplaced
			}
			index, err := foreignKeyIndex(view, target, c.Referenced)
			if err != nil {
				return nil, err
			}
			c.Index = index.address
			if _, err := bindForeignKeyComparison(view, next, target, index, *c); err != nil {
				return nil, err
			}
			c.OID, err = e.allocateOIDLocked()
			if err != nil {
				return nil, err
			}
		}
		if next != nil {
			var err error
			next.revision, err = e.revisionLocked()
			if err != nil {
				return nil, err
			}
			if err := stage.Replace(next); err != nil {
				return nil, err
			}
			if next.address == parent.address {
				parent = next
			}
		}
	}
	return parent, nil
}

func (s *StatementScope) acquireForeignKeyTypeProviders(view CatalogRead, address ObjectAddress) error {
	var refs []ObjectRef
	e := s.op.session.Engine()
	for _, descriptor := range view.state.descriptors {
		child, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		for _, f := range child.foreignKeys {
			if child.address != address && f.Target != address {
				continue
			}
			for _, a := range []ObjectAddress{child.address, f.Target} {
				ref := ObjectRef{owner: e.owner, Epoch: view.Epoch(), Address: a}
				if !slices.Contains(refs, ref) {
					refs = append(refs, ref)
				}
			}
		}
	}
	slices.SortFunc(refs, func(a, b ObjectRef) int {
		if a.Address.ObjectID < b.Address.ObjectID {
			return -1
		}
		if a.Address.ObjectID > b.Address.ObjectID {
			return 1
		}
		return 0
	})
	for _, ref := range refs {
		if err := s.foreignKeyGrant(ref, 0, relationRowShare, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) validateAlterForeignKeys(view CatalogRead, address ObjectAddress) error {
	auth, err := s.op.Authorization()
	if err != nil {
		return err
	}
	auth.catalog = view
	auth.recaptureCatalog(auth.catalog)
	e := s.op.session.Engine()
	for _, descriptor := range view.state.descriptors {
		child, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		for _, f := range child.foreignKeys {
			if f.NotValid || child.address != address && f.Target != address {
				continue
			}
			parent := view.state.descriptors[f.Target].(*relationDescriptor)
			index := view.state.descriptors[f.Index].(*relationDescriptor)
			comparisons, err := bindForeignKeyComparison(view, child, parent, index, f)
			if err != nil {
				return err
			}
			children, err := s.captureForeignKeyRows(ObjectRef{owner: e.owner, Epoch: view.Epoch(), Address: child.address}, ObjectAddress{})
			if err != nil {
				return err
			}
			parents, err := s.captureForeignKeyRows(ObjectRef{owner: e.owner, Epoch: view.Epoch(), Address: parent.address}, f.Index)
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
	var refs []ObjectRef
	for address, descriptor := range stage.descriptors {
		child, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		for _, f := range child.foreignKeys {
			if installedForeignKey(stage, address, f) {
				continue
			}
			if f.Target == address {
				continue
			}
			if _, external := stage.base.state.descriptors[f.Target]; !external {
				continue
			}
			ref := ObjectRef{owner: stage.owner.owner, Epoch: stage.base.Epoch(), Address: f.Target}
			if !slices.Contains(refs, ref) {
				refs = append(refs, ref)
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	slices.SortFunc(refs, func(a, b ObjectRef) int {
		if a.Address.ObjectID < b.Address.ObjectID {
			return -1
		}
		if a.Address.ObjectID > b.Address.ObjectID {
			return 1
		}
		return 0
	})
	for _, ref := range refs {
		if err := s.foreignKeyGrant(ref, 0, shareRowExclusive, nil); err != nil {
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
	for address, descriptor := range stage.descriptors {
		child, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		for _, f := range child.foreignKeys {
			if installedForeignKey(stage, address, f) {
				continue
			}
			if f.Target == address {
				continue
			}
			if _, external := stage.base.state.descriptors[f.Target]; !external {
				continue
			}
			parent, ok := auth.catalog.state.descriptors[f.Target].(*relationDescriptor)
			if !ok {
				return ErrCatalogReplaced
			}
			index, ok := auth.catalog.state.descriptors[f.Index].(*relationDescriptor)
			if !ok {
				return ErrCatalogReplaced
			}
			eligible, err := foreignKeyIndexEligible(parent, index, f.Referenced)
			if err != nil {
				return err
			}
			if !eligible {
				return ErrCatalogReplaced
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
