package engine

import (
	"errors"
	"fmt"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Resolve original authority before waiting; never resolve a replacement by name.
func (s *StatementScope) acquireRelation(stmt parser.Statement, execute bool) error {
	var name parser.ObjectName
	var mode relationLockMode
	ownerOnly := false
	policy := relationLockPolicy{timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
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
	case *parser.AlterTableDropColumn:
		if !execute {
			return nil
		}
		name = v.Name
		mode = accessExclusive
		ownerOnly = true
	case *parser.AlterTableNotNull:
		if !execute {
			return nil
		}
		name = v.Name
		mode = accessExclusive
		ownerOnly = true
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
		policy.nowait = v.Nowait
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
		if mode == accessShare || mode == rowExclusive {
			ref, err = e.resolveReference(auth, name)
		} else {
			ref, err = e.resolveRelationReference(auth, name, mode == shareUpdateExclusive)
		}
	}
	if err == nil {
		if ownerOnly {
			err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
		} else if mode == accessExclusive {
			actor, roleErr := auth.roles.Descriptor(auth.state.CurrentUser)
			if roleErr != nil {
				err = roleErr
			} else if !actor.Superuser {
				err = sqlError("42501", fmt.Sprintf("permission denied for table %s", name.Parts[len(name.Parts)-1].Text))
			}
		}
	}
	e.mu.Unlock()
	// Catalog provider validation may call Reset; ALTER must not retain a
	// replacement read admission across validation or publication.
	if ownerOnly && s.replacementRead {
		e.replacementMu.RUnlock()
		s.replacementRead = false
	}
	if err != nil {
		return err
	}
	request, err := e.locks.startAcquire(s.op.Context(), s.ticket, ref, mode, policy)
	if errors.Is(err, errRelationUnavailable) {
		relationName := name.Parts[0].Text
		if len(name.Parts) == 2 {
			relationName += "." + name.Parts[1].Text
		}
		return sqlError("55P03", fmt.Sprintf("could not obtain lock on relation \"%s\"", relationName))
	}
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.wait()
	}
	if err != nil {
		// Parse analysis opens SELECT/INSERT relations with a positioned
		// error callback. Cached execution and utility locks do not. Query
		// cancellation (57014) deliberately bypasses that callback.
		if (mode == accessShare || mode == rowExclusive) && (!execute || s.Expected == nil) {
			var diagnostic *sqlstate.Error
			if errors.As(err, &diagnostic) && diagnostic.Code == "55P03" && name.Parts[0].Pos.Character > 0 {
				copy := *diagnostic
				copy.Position = strconv.Itoa(name.Parts[0].Pos.Character)
				return &copy
			}
		}
		return err
	}
	s.owner.mu.Lock()
	implicit := s.owner.state == TxImplicit
	txref := s.owner.ref
	s.owner.mu.Unlock()
	if implicit && !ownerOnly && !s.replacementRead {
		e.replacementMu.RLock()
		s.replacementRead = true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	descriptor, live := e.state.descriptors[ref.Address].(*relationDescriptor)
	if ref.owner != e.owner || ref.Epoch != e.state.epoch || txref.epoch != e.state.epoch || !live {
		return ErrCatalogReplaced
	}
	s.lockedRef = ref
	s.lockedDescriptor = descriptor
	s.lockedIdentityOnly = mode == shareUpdateExclusive
	// Definition evolution while waiting is valid for this exact relation.
	// Pin the grant-time descriptor and revalidate current role authority.
	auth, err = e.statementAuthorizationLocked(s.op, stmt)
	if err != nil {
		return err
	}
	if ownerOnly {
		return auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
	} else if mode == accessExclusive {
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
