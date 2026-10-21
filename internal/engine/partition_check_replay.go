package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// This capability is an exact private-import target, not a public hierarchy
// ALTER policy. The ordinary ALTER binder, providers, stage and Finish still own
// every definition and publication check.
func (e *Engine) beginPartitionCheckReplay(op *session.Operation, raw *parser.AlterTable) error {
	if op == nil {
		return fmt.Errorf("stuntgres: invalid trusted restore partition CHECK operation")
	}
	operationReplay(op).partitionCheck = nil
	if !partitionCheckReplayActions(raw) {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.statementAuthorizationLocked(op, raw)
	if err != nil {
		return err
	}
	if !partitionConstraintReplayImport(e, op, auth) {
		return fmt.Errorf("stuntgres: invalid trusted restore partition CHECK authority")
	}
	if err := table.RestoreAlterConstraints(raw); err != nil {
		return err
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, true)
	if err != nil {
		return err
	}
	d, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	if d.RelationKind == catalog.TableRelation && d.Partition == nil && d.Membership != nil && d.Inheritance == nil {
		operationReplay(op).partitionCheck = d
	}
	return nil
}

func partitionCheckReplayActions(raw *parser.AlterTable) bool {
	if raw == nil || len(raw.Actions) == 0 {
		return false
	}
	for _, action := range raw.Actions {
		if action.Action != parser.AddCheck || action.Check == nil || !action.Check.NotValid {
			return false
		}
	}
	return true
}

// Callers hold Engine.mu, as do the existing hierarchy admission guards.
func partitionConstraintReplayImport(e *Engine, op *session.Operation, auth AuthorizationView) bool {
	if e == nil || op == nil || op.Self != op || op.Session == nil || engineOf(op.Session) != e || e.importTargets == nil || auth.engine != e || auth.session != op.Session || auth.RoleRecord != e.roles.published || auth.Catalog.Owner() == nil || auth.Catalog.Owner().Domain != e.catalog.Domain || auth.Catalog.State() != e.catalog.state || auth.Catalog.Epoch() != e.catalog.state.Epoch() {
		return false
	}
	if err := e.validateOperation(op); err != nil {
		return false
	}
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	return err == nil && bootstrap.Superuser
}

func partitionCheckReplayAllowed(op *session.Operation, auth AuthorizationView, raw *parser.AlterTable, d *catalog.Relation) bool {
	return op != nil && operationReplayOrZero(op).partitionCheck == d && d != nil && d.RelationKind == catalog.TableRelation && d.Partition == nil && d.Membership != nil && d.Inheritance == nil && auth.Catalog.Get(d.Address()) == d && partitionCheckReplayActions(raw) && partitionConstraintReplayImport(auth.engine, op, auth)
}
