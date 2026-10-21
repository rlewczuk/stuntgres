package engine

import (
	"cmp"
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (s *StatementScope) acquireIndexMaintenance(stmt parser.Statement) error {
	e := s.engine()
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
	g := &indexMaintenanceGrant{descriptors: make(map[ids.ObjectAddress]*catalog.Relation)}
	e.mu.Lock()
	fail := func(err error) error { e.mu.Unlock(); return err }
	if s.txRef.Epoch() != e.catalog.state.Epoch() {
		return fail(ids.ErrCatalogReplaced)
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
		d := auth.Catalog.MustRelation(ref.Address)
		g.targets = append(g.targets, ref)
		g.descriptors[ref.Address] = d
	}
	// Check the entire requested group before collecting parents or waiting.
	// Wrong-kind targets must never reach a sqlIndex dereference.
	if err := g.check(auth.View, auth, stmt, false); err != nil {
		return fail(err)
	}
	for _, ref := range g.targets {
		d := g.descriptors[ref.Address]
		switch v := stmt.(type) {
		case *parser.DropIndex:
			g.indexes = append(g.indexes, ref)
			parent := d.SQLIndex.Parent
			if g.descriptors[parent] == nil {
				g.parents = append(g.parents, ref.Domain().Ref(ref.Epoch, parent))
				g.descriptors[parent] = auth.Catalog.MustRelation(parent)
			}
		case *parser.Reindex:
			parent := d.Address()
			if v.Kind.Text == "index" {
				parent = d.SQLIndex.Parent
				g.indexes = append(g.indexes, ref)
			}
			g.parents = append(g.parents, ref.Domain().Ref(ref.Epoch, parent))
			g.descriptors[parent] = auth.Catalog.MustRelation(parent)
			if v.Kind.Text == "table" {
				g.indexSets = map[ids.ObjectAddress]int{parent: 0}
				for address, descriptor := range auth.Catalog.Descriptors() {
					index, ok := descriptor.(*catalog.Relation)
					if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != parent {
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
		child := auth.Catalog.MustRelation(ref.Address)
		if child.SQLIndex == nil {
			diagnostic := indexWrongKind(child, false).(*sqlstate.Error)
			diagnostic.Code = "42P17"
			return fail(diagnostic)
		}
		g.attach = ref
		g.descriptors[ref.Address] = child
		g.indexes = append(g.indexes, ref)
		parent := g.descriptors[g.targets[0].Address]
		if err := partitionIndexAttachCheck(s.op, auth.Catalog, parent, child); err != nil {
			return fail(err)
		}
	}
	if err := g.collectPartitionIndexes(auth.View, stmt); err != nil {
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
				return fail(validateIndexOptions(v, g.descriptors[g.targets[0].Address].SQLIndex.Method))
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
	s.indexes().maintenance = g
	e.mu.Unlock()
	if s.replacementRead {
		e.replacementMu.RUnlock()
		s.replacementRead = false
	}
	slices.SortFunc(g.parents, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	slices.SortFunc(g.indexes, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
	acquire := func(ref ids.ObjectRef, mode lock.Mode) error {
		request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: ref}, mode, policy)
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
		if s.txRef.Epoch() != e.catalog.state.Epoch() {
			return ids.ErrCatalogReplaced
		}
		if err := g.check(auth.View, auth, stmt, false); err != nil {
			return err
		}
		for address, observed := range g.descriptors {
			current := auth.Catalog.MustRelation(address)
			if g.partition && current != observed {
				return ids.ErrCatalogReplaced
			}
			g.descriptors[address] = current
		}
		return nil
	}
	parentMode := lock.Share
	if drop || g.attach != (ids.ObjectRef{}) {
		parentMode = lock.AccessExclusive
	}
	for _, ref := range g.parents {
		if err := acquire(ref, parentMode); err != nil {
			return err
		}
	}
	for _, ref := range g.indexes {
		if err := acquire(ref, lock.AccessExclusive); err != nil {
			return err
		}
	}
	if _, alter := stmt.(*parser.AlterIndex); alter && len(g.targets) > 0 {
		ref := g.targets[0]
		mode := lock.AccessExclusive
		if g.descriptors[ref.Address].RelationKind == catalog.IndexRelation {
			mode = lock.ShareUpdateExclusive
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
