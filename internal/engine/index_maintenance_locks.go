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
		if err := ValidateReindexOptions(v); err != nil {
			return err
		}
		if v.Kind.Text != "index" && v.Kind.Text != "table" {
			return ReindexControlError(v)
		}
		names = []parser.ObjectName{v.Name}
	}
	g := &indexMaintenanceGrant{Descriptors: make(map[ids.ObjectAddress]*catalog.Relation)}
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
			g.Notices = append(g.Notices, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`%s "%s" does not exist, skipping`, kind, missing)})
			continue
		}
		if g.Descriptors[ref.Address] != nil {
			continue
		}
		d := auth.Catalog.MustRelation(ref.Address)
		g.Targets = append(g.Targets, ref)
		g.Descriptors[ref.Address] = d
	}
	// Check the entire requested group before collecting parents or waiting.
	// Wrong-kind targets must never reach a sqlIndex dereference.
	if err := g.Check(auth.View, auth, stmt, false); err != nil {
		return fail(err)
	}
	for _, ref := range g.Targets {
		d := g.Descriptors[ref.Address]
		switch v := stmt.(type) {
		case *parser.DropIndex:
			g.Indexes = append(g.Indexes, ref)
			parent := d.SQLIndex.Parent
			if g.Descriptors[parent] == nil {
				g.Parents = append(g.Parents, ref.Domain().Ref(ref.Epoch, parent))
				g.Descriptors[parent] = auth.Catalog.MustRelation(parent)
			}
		case *parser.Reindex:
			parent := d.Address()
			if v.Kind.Text == "index" {
				parent = d.SQLIndex.Parent
				g.Indexes = append(g.Indexes, ref)
			}
			g.Parents = append(g.Parents, ref.Domain().Ref(ref.Epoch, parent))
			g.Descriptors[parent] = auth.Catalog.MustRelation(parent)
			if v.Kind.Text == "table" {
				g.IndexSets = map[ids.ObjectAddress]int{parent: 0}
				for address, descriptor := range auth.Catalog.Descriptors() {
					index, ok := descriptor.(*catalog.Relation)
					if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != parent {
						continue
					}
					g.Indexes = append(g.Indexes, ref.Domain().Ref(ref.Epoch, address))
					g.Descriptors[address] = index
					g.IndexSets[parent]++
				}
			}
		}
	}
	if v, ok := stmt.(*parser.AlterIndex); ok && v.Action == "attach" && len(g.Targets) > 0 {
		ref, err := e.resolveRelationReference(auth, v.Partition, true)
		if err != nil {
			return fail(err)
		}
		child := auth.Catalog.MustRelation(ref.Address)
		if child.SQLIndex == nil {
			diagnostic := WrongKind(child, false).(*sqlstate.Error)
			diagnostic.Code = "42P17"
			return fail(diagnostic)
		}
		g.Attach = ref
		g.Descriptors[ref.Address] = child
		g.Indexes = append(g.Indexes, ref)
		parent := g.Descriptors[g.Targets[0].Address]
		if err := PartitionIndexAttachCheck(s.op, auth.Catalog, parent, child); err != nil {
			return fail(err)
		}
	}
	if err := g.CollectPartitionIndexes(auth.View, stmt); err != nil {
		return fail(err)
	}
	if len(g.Targets) > 0 {
		switch v := stmt.(type) {
		case *parser.DropIndex:
			if v.Concurrent.Character > 0 {
				return fail(Capability("concurrent index operations are unsupported", v.Concurrent))
			}
		case *parser.AlterIndex:
			switch v.Action {
			case "set", "reset":
				return fail(ValidateIndexOptions(v, g.Descriptors[g.Targets[0].Address].SQLIndex.Method))
			case "tablespace":
				return fail(Capability("index tablespaces are unsupported", v.ActionPos))
			case "statistics":
				return fail(Capability("index statistics are unsupported", v.ActionPos))
			}
		case *parser.Reindex:
			if err := ReindexControlError(v); err != nil {
				return fail(err)
			}
		}
	}
	s.indexes().Maintenance = g
	e.mu.Unlock()
	if s.replacementRead {
		e.replacementMu.RUnlock()
		s.replacementRead = false
	}
	slices.SortFunc(g.Parents, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	slices.SortFunc(g.Indexes, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
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
		if err := g.Check(auth.View, auth, stmt, false); err != nil {
			return err
		}
		for address, observed := range g.Descriptors {
			current := auth.Catalog.MustRelation(address)
			if g.Partition && current != observed {
				return ids.ErrCatalogReplaced
			}
			g.Descriptors[address] = current
		}
		return nil
	}
	parentMode := lock.Share
	if drop || g.Attach != (ids.ObjectRef{}) {
		parentMode = lock.AccessExclusive
	}
	for _, ref := range g.Parents {
		if err := acquire(ref, parentMode); err != nil {
			return err
		}
	}
	for _, ref := range g.Indexes {
		if err := acquire(ref, lock.AccessExclusive); err != nil {
			return err
		}
	}
	if _, alter := stmt.(*parser.AlterIndex); alter && len(g.Targets) > 0 {
		ref := g.Targets[0]
		mode := lock.AccessExclusive
		if g.Descriptors[ref.Address].RelationKind == catalog.IndexRelation {
			mode = lock.ShareUpdateExclusive
		}
		if !slices.Contains(g.Indexes, ref) {
			if err := acquire(ref, mode); err != nil {
				return err
			}
		}
		s.lockedRef = ref
		s.lockedDescriptor = g.Descriptors[ref.Address]
		s.lockedIdentityOnly = true
	}
	return nil
}
