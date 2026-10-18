package engine

import (
	"cmp"
	"fmt"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Resolved identities survive waits. The descriptor observations are refreshed
// at grant time, then fenced during execution/publication; names are not retried.
type indexMaintenanceGrant struct {
	targets     []ids.ObjectRef
	parents     []ids.ObjectRef
	indexes     []ids.ObjectRef
	descriptors map[ids.ObjectAddress]*relationDescriptor
	indexSets   map[ids.ObjectAddress]int
	notices     []sqlstate.Error
	attach      ids.ObjectRef
	partition   bool
}

func (g *indexMaintenanceGrant) check(auth AuthorizationView, stmt parser.Statement, strict bool) error {
	for address, observed := range g.descriptors {
		current, ok := auth.catalog.state.descriptors[address].(*relationDescriptor)
		if !ok || strict && current != observed {
			return ErrCatalogReplaced
		}
	}
	for _, ref := range g.targets {
		if ref.Domain() != auth.engine.domain || ref.Epoch != auth.catalog.Epoch() {
			return ErrCatalogReplaced
		}
		d := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		switch s := stmt.(type) {
		case *parser.DropIndex:
			if d.relationKind != indexRelation {
				return indexWrongKind(d, true)
			}
			if err := relationDropOwner(auth, ref, d); err != nil {
				return err
			}
			if err := partitionIndexDropDependency(auth.catalog, d); err != nil {
				return err
			}
			if d.sqlIndex.constraint != nil {
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: fmt.Sprintf("cannot drop index %s because constraint %s on table %s requires it", d.name, d.sqlIndex.constraint.Name, auth.catalog.state.descriptors[d.sqlIndex.parent].Name()), Hint: "You can drop constraint " + d.sqlIndex.constraint.Name + " on table " + auth.catalog.state.descriptors[d.sqlIndex.parent].Name() + " instead."}
			}
		case *parser.AlterIndex:
			if s.Action != "rename" && d.relationKind != indexRelation {
				return indexWrongKind(d, false)
			}
			if err := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
				return err
			}
			if s.Action == "rename" {
				if err := auth.CheckCreate(ref.Domain().Ref(ref.Epoch, d.namespace)); err != nil {
					return err
				}
			}
		case *parser.Reindex:
			parent := d
			if s.Kind.Text == "index" {
				if d.relationKind != indexRelation {
					return indexWrongKind(d, false)
				}
				parent = auth.catalog.state.descriptors[d.sqlIndex.parent].(*relationDescriptor)
			} else if d.relationKind != tableRelation && d.relationKind != partitionedRelation {
				return sqlError("42809", fmt.Sprintf(`"%s" is not a table or materialized view`, d.name))
			}
			rights, err := auth.Effective(ref.Domain().Ref(ref.Epoch, parent.address), false)
			if err != nil {
				return err
			}
			if rights&authorization.MaintainPrivilege == 0 {
				return sqlError("42501", fmt.Sprintf("permission denied for %s %s", s.Kind.Text, d.name))
			}
			if parent.partition != nil {
				return sqlError("0A000", "partitioned indexes are unsupported")
			}
		}
	}
	for _, ref := range g.indexes {
		d := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if d.sqlIndex == nil || g.descriptors[d.sqlIndex.parent] == nil {
			return ErrCatalogReplaced
		}
	}
	for parent, count := range g.indexSets {
		actual := 0
		for address, descriptor := range auth.catalog.state.descriptors {
			d, ok := descriptor.(*relationDescriptor)
			if !ok || d.sqlIndex == nil || d.sqlIndex.parent != parent {
				continue
			}
			if g.descriptors[address] == nil {
				return ErrCatalogReplaced
			}
			actual++
		}
		if actual != count {
			return ErrCatalogReplaced
		}
	}
	return nil
}

func indexWrongKind(d *relationDescriptor, drop bool) error {
	diagnostic := sqlError("42809", fmt.Sprintf(`"%s" is not an index`, d.name)).(*sqlstate.Error)
	if drop {
		switch d.relationKind {
		case tableRelation, partitionedRelation:
			diagnostic.Hint = "Use DROP TABLE to remove a table."
		case sequenceRelation:
			diagnostic.Hint = "Use DROP SEQUENCE to remove a sequence."
		case viewRelation:
			diagnostic.Hint = "Use DROP VIEW to remove a view."
		}
	}
	return diagnostic
}

func (s *StatementScope) acquireIndexMaintenance(stmt parser.Statement) error {
	e := s.op.session.Engine()
	var names []parser.ObjectName
	var ifExists, drop bool
	switch v := stmt.(type) {
	case *parser.DropIndex:
		names = v.Names
		ifExists = v.IfExists
		drop = true
	case *parser.AlterIndex:
		names = []parser.ObjectName{v.Name}
		ifExists = v.IfExists
	case *parser.Reindex:
		if err := validateReindexOptions(v); err != nil {
			return err
		}
		if v.Kind.Text != "index" && v.Kind.Text != "table" {
			return reindexControlError(v)
		}
		names = []parser.ObjectName{v.Name}
	}
	g := &indexMaintenanceGrant{descriptors: make(map[ids.ObjectAddress]*relationDescriptor)}
	e.mu.Lock()
	fail := func(err error) error { e.mu.Unlock(); return err }
	if s.txRef.Epoch() != e.state.epoch {
		return fail(ErrCatalogReplaced)
	}
	auth, err := e.statementAuthorizationLocked(s.op, stmt)
	if err != nil {
		return fail(err)
	}
	for _, name := range names {
		ref, err := e.resolveRelationReference(auth, name, true)
		if err != nil {
			if !renameMissing(err) {
				return fail(err)
			}
			kind, missing := "relation", name.Parts[len(name.Parts)-1].Text
			diagnostic := err.(*sqlstate.Error)
			if diagnostic.Code == "3F000" {
				kind, missing = "schema", name.Parts[0].Text
			} else if drop {
				kind = "index"
				err = sqlError("42704", fmt.Sprintf(`index "%s" does not exist`, missing))
			}
			if !ifExists {
				return fail(err)
			}
			g.notices = append(g.notices, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`%s "%s" does not exist, skipping`, kind, missing)})
			continue
		}
		if g.descriptors[ref.Address] != nil {
			continue
		}
		d := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		g.targets = append(g.targets, ref)
		g.descriptors[ref.Address] = d
	}
	// Check the entire requested group before collecting parents or waiting.
	// Wrong-kind targets must never reach a sqlIndex dereference.
	if err := g.check(auth, stmt, false); err != nil {
		return fail(err)
	}
	for _, ref := range g.targets {
		d := g.descriptors[ref.Address]
		switch v := stmt.(type) {
		case *parser.DropIndex:
			g.indexes = append(g.indexes, ref)
			parent := d.sqlIndex.parent
			if g.descriptors[parent] == nil {
				g.parents = append(g.parents, ref.Domain().Ref(ref.Epoch, parent))
				g.descriptors[parent] = auth.catalog.state.descriptors[parent].(*relationDescriptor)
			}
		case *parser.Reindex:
			parent := d.address
			if v.Kind.Text == "index" {
				parent = d.sqlIndex.parent
				g.indexes = append(g.indexes, ref)
			}
			g.parents = append(g.parents, ref.Domain().Ref(ref.Epoch, parent))
			g.descriptors[parent] = auth.catalog.state.descriptors[parent].(*relationDescriptor)
			if v.Kind.Text == "table" {
				g.indexSets = map[ids.ObjectAddress]int{parent: 0}
				for address, descriptor := range auth.catalog.state.descriptors {
					index, ok := descriptor.(*relationDescriptor)
					if !ok || index.sqlIndex == nil || index.sqlIndex.parent != parent {
						continue
					}
					g.indexes = append(g.indexes, ref.Domain().Ref(ref.Epoch, address))
					g.descriptors[address] = index
					g.indexSets[parent]++
				}
			}
		}
	}
	if v, ok := stmt.(*parser.AlterIndex); ok && v.Action == "attach" && len(g.targets) > 0 {
		ref, err := e.resolveRelationReference(auth, v.Partition, true)
		if err != nil {
			return fail(err)
		}
		child := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if child.sqlIndex == nil {
			diagnostic := indexWrongKind(child, false).(*sqlstate.Error)
			diagnostic.Code = "42P17"
			return fail(diagnostic)
		}
		g.attach = ref
		g.descriptors[ref.Address] = child
		g.indexes = append(g.indexes, ref)
		parent := g.descriptors[g.targets[0].Address]
		if err := partitionIndexAttachCheck(s.op, auth.catalog, parent, child); err != nil {
			return fail(err)
		}
	}
	if err := g.collectPartitionIndexes(auth, stmt); err != nil {
		return fail(err)
	}
	if len(g.targets) > 0 {
		switch v := stmt.(type) {
		case *parser.DropIndex:
			if v.Concurrent.Character > 0 {
				return fail(indexCapability("concurrent index operations are unsupported", v.Concurrent))
			}
		case *parser.AlterIndex:
			switch v.Action {
			case "set", "reset":
				return fail(validateIndexOptions(v, g.descriptors[g.targets[0].Address].sqlIndex.method))
			case "tablespace":
				return fail(indexCapability("index tablespaces are unsupported", v.ActionPos))
			case "statistics":
				return fail(indexCapability("index statistics are unsupported", v.ActionPos))
			}
		case *parser.Reindex:
			if err := reindexControlError(v); err != nil {
				return fail(err)
			}
		}
	}
	s.indexMaintenance = g
	e.mu.Unlock()
	if s.replacementRead {
		e.replacementMu.RUnlock()
		s.replacementRead = false
	}
	slices.SortFunc(g.parents, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	slices.SortFunc(g.indexes, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	policy := relationLockPolicy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
	acquire := func(ref ids.ObjectRef, mode relationLockMode) error {
		request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lockResource{ObjectRef: ref}, mode, policy)
		if request != nil {
			err = request.Wait()
		}
		if err != nil {
			return err
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		auth, err := e.statementAuthorizationLocked(s.op, stmt)
		if err != nil {
			return err
		}
		if s.txRef.Epoch() != e.state.epoch {
			return ErrCatalogReplaced
		}
		if err := g.check(auth, stmt, false); err != nil {
			return err
		}
		for address, observed := range g.descriptors {
			current := auth.catalog.state.descriptors[address].(*relationDescriptor)
			if g.partition && current != observed {
				return ErrCatalogReplaced
			}
			g.descriptors[address] = current
		}
		return nil
	}
	parentMode := relationShare
	if drop || g.attach != (ids.ObjectRef{}) {
		parentMode = accessExclusive
	}
	for _, ref := range g.parents {
		if err := acquire(ref, parentMode); err != nil {
			return err
		}
	}
	for _, ref := range g.indexes {
		if err := acquire(ref, accessExclusive); err != nil {
			return err
		}
	}
	if _, alter := stmt.(*parser.AlterIndex); alter && len(g.targets) > 0 {
		ref := g.targets[0]
		mode := accessExclusive
		if g.descriptors[ref.Address].relationKind == indexRelation {
			mode = shareUpdateExclusive
		}
		if !slices.Contains(g.indexes, ref) {
			if err := acquire(ref, mode); err != nil {
				return err
			}
		}
		s.lockedRef = ref
		s.lockedDescriptor = g.descriptors[ref.Address]
		s.lockedIdentityOnly = true
	}
	return nil
}
