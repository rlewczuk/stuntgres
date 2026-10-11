package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func partitionConsumerBoundary(op *Operation, auth AuthorizationView, statement parser.Statement, d *relationDescriptor) error {
	if !hierarchyRelation(d) {
		return nil
	}
	message := ""
	switch s := statement.(type) {
	case *parser.Update:
		message = "updating partition hierarchies is unsupported"
	case *parser.Delete:
		message = "deleting from partition hierarchies is unsupported"
	case *parser.Select:
		if s.Locking != nil {
			message = "row locking partition hierarchies is unsupported"
		}
	case *parser.LockTable:
		message = "locking partition hierarchies is unsupported"
	case *parser.CreateIndex:
		if d.partition != nil && !s.Unique {
			message = "partitioned indexes are unsupported"
		}
	case *parser.Reindex:
		if d.partition != nil {
			message = "partitioned indexes are unsupported"
		}
	case *parser.AlterTable:
		if rowSecurityActions(s.Actions) {
			return nil
		}
		if partitionConstraintDropActions(d, s.Actions) {
			return nil
		}
		for _, action := range s.Actions {
			if action.Action != parser.DropColumn {
				continue
			}
			if s.Only && d.partition != nil && len(d.partition.children) != 0 {
				err := sqlError("42P16", "cannot drop column from only the partitioned table when partitions exist").(*sqlstate.Error)
				err.Hint = "Do not specify the ONLY keyword."
				return err
			}
			if slot, ok := d.index[action.Column.Text]; ok && partitionKeyUsesAttribute(d, d.attributes[slot]) {
				return sqlError("42P16", fmt.Sprintf(`cannot drop column "%s" because it is part of the partition key of relation "%s"`, action.Column.Text, d.name))
			}
		}
		message = "altering partition hierarchies is unsupported"
	case *parser.AlterTableDefault:
		if !partitionDefaultReplayAllowed(auth.engine, op, auth, s) {
			message = "altering partition hierarchies is unsupported"
		}
	case *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableSetSchema:
		message = "altering partition hierarchies is unsupported"
	}
	if message != "" {
		return sqlError("0A000", message)
	}
	return nil
}

// These newly admitted mutation forms finish ordinary analysis and ACL checks,
// but must not demand defaults, capture virtual storage or publish routed effects.
func partitionWriteExtensionBoundary(statement parser.Statement, d *relationDescriptor) error {
	if d == nil || !hierarchyRelation(d) {
		return nil
	}
	switch syntax := statement.(type) {
	case *parser.Insert:
		if syntax.Conflict != nil {
			return sqlError("0A000", "ON CONFLICT on partition hierarchies is unsupported")
		}
	case *parser.Merge:
		return sqlError("0A000", "merging partition hierarchies is unsupported")
	}
	return nil
}

func partitionDefaultReplayAllowed(e *Engine, op *Operation, auth AuthorizationView, statement *parser.AlterTableDefault) bool {
	if e == nil || op == nil || op.self != op || !op.restoreDefaultReplay || op.session == nil || op.session.Engine() != e || e.importTargets == nil || auth.engine != e || auth.session != op.session || auth.roles.state != e.roles || auth.catalog.owner == nil || auth.catalog.owner.owner != e.owner || auth.catalog.state != e.state || auth.catalog.Epoch() != e.state.epoch || statement == nil || statement.Action != parser.SetDefault || statement.Expression == nil {
		return false
	}
	if err := e.validateOperation(op); err != nil {
		return false
	}
	bootstrap, err := auth.roles.Descriptor(auth.state.Authenticated)
	return err == nil && bootstrap.Superuser
}

// Dump may replay differently owned members. Only the private, authenticated
// importer can open their parent as its bootstrap principal; the child owner
// and all type and scalar-expression authority remain the named CurrentUser.
func (e *Engine) partitionParentAuthorization(op *Operation, auth AuthorizationView, syntax *parser.CreateTable) (AuthorizationView, error) {
	if syntax == nil || syntax.PartitionOf == nil || op == nil || !op.restoreTableDestination {
		return auth, nil
	}
	if e == nil || op.self != op || op.session == nil || op.session.Engine() != e || e.importTargets == nil || auth.engine != e || auth.session != op.session || auth.roles.state != e.roles || auth.catalog.owner == nil || auth.catalog.owner.owner != e.owner || auth.catalog.state != e.state || auth.catalog.Epoch() != e.state.epoch {
		return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore partition parent authority")
	}
	if err := e.validateOperation(op); err != nil {
		return AuthorizationView{}, err
	}
	bootstrap, err := auth.roles.Descriptor(auth.state.Authenticated)
	if err != nil || !bootstrap.Superuser {
		return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore partition parent authority")
	}
	parentAuth := auth
	parentAuth.state.CurrentUser = bootstrap.Ref
	return parentAuth, nil
}

func partitionKeyUsesAttribute(d *relationDescriptor, attribute int32) bool {
	if d.partition == nil {
		return false
	}
	for _, key := range d.partition.keys {
		if key.attribute == attribute {
			return true
		}
		if key.derived != nil {
			for _, ref := range key.derived.dependencies {
				if ref.Address.ClassID == RelationClassID && ref.Address.ObjectID == d.address.ObjectID && ref.Address.SubID == attribute {
					return true
				}
			}
		}
	}
	return false
}

func (s *StatementScope) acquirePartitionLock(ref ObjectRef, mode relationLockMode) error {
	e := s.op.session.Engine()
	request, err := e.locks.startAcquire(s.op.Context(), s.ticket, lockResource{ObjectRef: ref}, mode, relationLockPolicy{timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext})
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.wait()
	}
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return s.observePartitionRelationLocked(e, ref)
}

// SQL hierarchy locks stabilize membership; this identity fence also rejects
// metadata replacement through public control APIs that do not take SQL locks.
func (s *StatementScope) observePartitionRelationLocked(e *Engine, ref ObjectRef) error {
	catalog := e.privateCatalogLocked(s.owner.catalog)
	d, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || ref.owner != e.owner || ref.Epoch != catalog.Epoch() {
		return ErrCatalogReplaced
	}
	if old := s.partitionRelations[ref.Address]; old != nil && old != d {
		return ErrCatalogReplaced
	}
	if hierarchyRelation(d) {
		if s.partitionRelations == nil {
			s.partitionRelations = make(map[ObjectAddress]*relationDescriptor)
		}
		s.partitionRelations[ref.Address] = d
	}
	return nil
}

// Ancestors are opened root-first before a leaf lock. In particular a direct
// DEFAULT writer must not hold leaf RX while waiting for its parent's AS:
// a creator owns parent AX before requesting DEFAULT AX.
func (s *StatementScope) acquirePartitionAncestors(ref ObjectRef) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	catalog := e.privateCatalogLocked(s.owner.catalog)
	d, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || ref.Epoch != catalog.Epoch() {
		e.mu.Unlock()
		return ErrCatalogReplaced
	}
	var ancestors []ObjectRef
	for d.membership != nil {
		d, ok = catalog.state.descriptors[d.membership.parent].(*relationDescriptor)
		if !ok {
			e.mu.Unlock()
			return ErrCatalogReplaced
		}
		ancestors = append(ancestors, ObjectRef{owner: ref.owner, Epoch: ref.Epoch, Address: d.address})
	}
	e.mu.Unlock()
	slices.Reverse(ancestors)
	for _, ancestor := range ancestors {
		if err := s.acquirePartitionLock(ancestor, accessShare); err != nil {
			return err
		}
	}
	return nil
}

// Each opened parent prevents membership mutation while its children are
// discovered. Prefix captures therefore retain one coherent hierarchy shape.
func (s *StatementScope) acquirePartitionDescendants(ref ObjectRef, mode relationLockMode) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	catalog := e.privateCatalogLocked(s.owner.catalog)
	d, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || ref.Epoch != catalog.Epoch() {
		e.mu.Unlock()
		return ErrCatalogReplaced
	}
	if err := s.observePartitionRelationLocked(e, ref); err != nil {
		e.mu.Unlock()
		return err
	}
	var children []ObjectAddress
	if d.partition != nil {
		children = d.partition.children
	}
	e.mu.Unlock()
	for _, address := range children {
		child := ObjectRef{owner: ref.owner, Epoch: ref.Epoch, Address: address}
		if err := s.acquirePartitionLock(child, mode); err != nil {
			return err
		}
		if err := s.acquirePartitionDescendants(child, mode); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) acquirePartitionDefinitionDefault(parent ObjectRef) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	catalog := e.privateCatalogLocked(s.owner.catalog)
	d, ok := catalog.state.descriptors[parent.Address].(*relationDescriptor)
	if !ok || parent.Epoch != catalog.Epoch() {
		e.mu.Unlock()
		return ErrCatalogReplaced
	}
	if err := s.observePartitionRelationLocked(e, parent); err != nil {
		e.mu.Unlock()
		return err
	}
	var child ObjectRef
	if d.partition != nil && d.partition.defaultChild != (ObjectAddress{}) {
		child = ObjectRef{owner: parent.owner, Epoch: parent.Epoch, Address: d.partition.defaultChild}
	}
	e.mu.Unlock()
	if child.owner == nil {
		return nil
	}
	if err := s.acquirePartitionLock(child, accessExclusive); err != nil {
		return err
	}
	return s.acquirePartitionDescendants(child, accessExclusive)
}
