package engine

import (
	"fmt"
	"stuntgres/internal/parser"
)

// Resolve original authority before waiting; never resolve a replacement by name.
func (s *StatementScope) acquireRelation(stmt parser.Statement, execute bool) error {
	var name parser.ObjectName
	var mode relationLockMode
	switch v := stmt.(type) {
	case *parser.Select:
		name = v.Name
		mode = accessShare
	case *parser.Insert:
		name = v.Name
		mode = rowExclusive
	case *parser.Comment:
		if !execute || (v.Kind != parser.CommentTable && v.Kind != parser.CommentColumn) {
			return nil
		}
		name = v.Name
		if v.Kind == parser.CommentColumn {
			if len(name.Parts) < 2 {
				return sqlError("42601", "column name must be qualified")
			}
			name.Parts = name.Parts[:len(name.Parts)-1]
		}
		mode = shareUpdateExclusive
	case *parser.LockTable:
		if !execute {
			return nil
		}
		s.owner.mu.Lock()
		active := s.owner.state == TxActive
		s.owner.mu.Unlock()
		if !active {
			return sqlError("25P01", "LOCK TABLE can only be used in transaction blocks")
		}
		name = v.Name
		mode = accessExclusive
	default:
		return nil
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	if s.txRef.epoch != e.state.epoch {
		e.mu.Unlock()
		return ErrCatalogReplaced
	}
	auth, err := e.statementAuthorizationLocked(s.op, stmt)
	var ref ObjectRef
	if err == nil {
		ref, err = e.resolveRelationReference(auth, name, mode == shareUpdateExclusive)
	}
	var descriptor Descriptor
	if err == nil {
		descriptor = e.state.descriptors[ref.Address]
		if mode == accessExclusive {
			actor, roleErr := auth.roles.Descriptor(auth.state.CurrentUser)
			if roleErr != nil {
				err = roleErr
			} else if !actor.Superuser {
				err = sqlError("42501", fmt.Sprintf("permission denied for table %s", name.Parts[len(name.Parts)-1].Text))
			}
		}
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	request, err := e.locks.startAcquire(s.op.Context(), s.ticket, ref, mode)
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
	s.owner.mu.Lock()
	implicit := s.owner.state == TxImplicit
	txref := s.owner.ref
	s.owner.mu.Unlock()
	if implicit && !s.replacementRead {
		e.replacementMu.RLock()
		s.replacementRead = true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	current := e.state.descriptors[ref.Address]
	if ref.Epoch != e.state.epoch || txref.epoch != e.state.epoch || current == nil {
		return ErrCatalogReplaced
	}
	if mode == shareUpdateExclusive {
		// A preceding COMMENT may publish a new immutable payload while this
		// request waits. Consume that payload at the captured identity, never
		// a fresh name lookup that could retarget a replacement relation.
		if _, ok := current.(*relationDescriptor); !ok {
			return ErrCatalogReplaced
		}
		descriptor = current
	} else if current != descriptor {
		return ErrCatalogReplaced
	}
	s.lockedRef = ref
	s.lockedDescriptor = descriptor
	s.lockedIdentityOnly = mode == shareUpdateExclusive
	// Revalidate current role authority as well as the immutable relation identity.
	auth, err = e.statementAuthorizationLocked(s.op, stmt)
	if err != nil {
		return err
	}
	if mode == accessExclusive {
		actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
		if err != nil {
			return err
		}
		if !actor.Superuser {
			return sqlError("42501", fmt.Sprintf("permission denied for table %s", name.Parts[len(name.Parts)-1].Text))
		}
	}
	return nil
}

func (s *StatementScope) validateLockedRelation(e *Engine) error {
	if s.lockedRef.owner == nil {
		return nil
	}
	if s.lockedRef.owner != e.owner || s.lockedRef.Epoch != e.state.epoch {
		return ErrCatalogReplaced
	}
	current := e.state.descriptors[s.lockedRef.Address]
	if s.lockedIdentityOnly {
		if _, ok := current.(*relationDescriptor); !ok {
			return ErrCatalogReplaced
		}
	} else if current != s.lockedDescriptor {
		return ErrCatalogReplaced
	}
	return nil
}
