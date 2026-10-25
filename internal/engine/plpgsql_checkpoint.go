package engine

import (
	"context"
	"errors"
	"maps"
	"slices"

	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
)

// An exception mark belongs to the admitted statement and the owner's existing
// chronological savepoint stack. It does not own publication, snapshots or IDs.
// Admission serializes it; provider callbacks below hold no publication mutex.
type plExceptionCheckpoint struct {
	scope                                       *StatementScope
	owner                                       *session.TransactionOwner
	serial                                      uint64
	rows                                        map[ids.ObjectAddress][]storage.RowVersion
	deletes                                     map[ids.ObjectAddress]*visibility.PrivateDeletes
	replacing                                   bool
	ownWrites                                   map[ids.ObjectAddress][]storage.RowVersion
	ownDeletes                                  map[ids.ObjectAddress]*visibility.PrivateDeletes
	catalog                                     *session.Journal
	catalogAuthority                            *session.CatalogAuthority
	foreignActions                              *dml.ForeignActionScope
	indexes                                     *index.Scope
	multiEvents                                 []lock.MultiEvent
	notificationActions                         []session.NotificationAction
	notificationEvents                          []session.Notification
	completed, sessionCompleted, projectedRoots bool
	foreignModifications                        int
	holdPromotions                              []*sqlCursor
	truncate                                    *dml.TruncateScope
	drops                                       StatementSlot
	foreignKeys                                 *table.ForeignKeyCreation
	viewCreation                                *view.CreationScope
	materialized                                *view.MaterializedRefreshScope
	disposed                                    bool
}

// A failed provider cleanup makes every enclosing block ineligible to catch:
// handlers must never execute against a partly restored foreign transaction.
// Unwrap retains both the originating SQL diagnostic and secondary evidence.
func exceptionRows(rows map[ids.ObjectAddress][]storage.RowVersion) map[ids.ObjectAddress][]storage.RowVersion {
	copy := maps.Clone(rows)
	for a, rows := range copy {
		copy[a] = rows[:len(rows):len(rows)]
	}
	return copy
}
func exceptionDeletes(deletes map[ids.ObjectAddress]*visibility.PrivateDeletes) map[ids.ObjectAddress]*visibility.PrivateDeletes {
	copy := maps.Clone(deletes)
	for a, root := range copy {
		if root != nil {
			copy[a] = &visibility.PrivateDeletes{Rows: maps.Clone(root.Rows)}
		}
	}
	return copy
}
func exceptionForeignActions(scope *dml.ForeignActionScope) *dml.ForeignActionScope {
	if scope == nil {
		return nil
	}
	copy := *scope
	copy.Admissions = maps.Clone(scope.Admissions)
	cloneJobs := func(jobs []dml.ForeignKeyJob) []dml.ForeignKeyJob {
		jobs = slices.Clone(jobs)
		for i := range jobs {
			jobs[i].ChildKey = slices.Clone(jobs[i].ChildKey)
		}
		return jobs
	}
	for a, admission := range scope.Admissions {
		if admission != nil {
			owned := *admission
			owned.Jobs = cloneJobs(owned.Jobs)
			copy.Admissions[a] = &owned
		}
	}
	copy.Transitions = slices.Clone(scope.Transitions)
	copy.Jobs = cloneJobs(scope.Jobs)
	return &copy
}
func exceptionIndexes(scope *index.Scope) *index.Scope {
	if scope == nil {
		return nil
	}
	copy := *scope
	copy.ExistingOutputs = maps.Clone(scope.ExistingOutputs)
	for a, outputs := range scope.ExistingOutputs {
		copy.ExistingOutputs[a] = maps.Clone(outputs)
	}
	copy.ExclusionModes.ByConstraint = maps.Clone(scope.ExclusionModes.ByConstraint)
	copy.ExclusionModes.Events = slices.Clone(scope.ExclusionModes.Events)
	return &copy
}

func (s *StatementScope) beginPLException() (*plExceptionCheckpoint, error) {
	t := s.Owner
	t.Mu.Lock()
	if err := t.Validate(s.TxRef); err != nil {
		t.Mu.Unlock()
		return nil, err
	}
	if t.SavepointSerial == ^uint64(0) {
		t.Mu.Unlock()
		return nil, errors.New("stuntgres: foreign savepoint identity exhausted")
	}
	mark := captureSavepoint(t, "")
	mark.Internal = true
	t.Mu.Unlock()
	s.foreignUnlocked()
	if err := foreignSavepoint(t, s.Op.Context(), 0, mark.Serial); err != nil {
		return nil, &proc.PLExceptionCleanupError{Primary: err, Cleanup: errors.New("exception mark acquisition failed")}
	}
	c := &plExceptionCheckpoint{scope: s, owner: t, serial: mark.Serial, rows: exceptionRows(s.Stage.Rows), deletes: exceptionDeletes(s.Stage.Deletes), replacing: s.Stage.replacing,
		ownWrites: exceptionRows(s.OwnWrites), ownDeletes: exceptionDeletes(s.OwnDeletes), catalog: s.Catalog, catalogAuthority: s.CatalogAuthority,
		foreignActions: exceptionForeignActions(s.foreignActionsSlot), indexes: exceptionIndexes(s.indexesSlot), multiEvents: slices.Clone(s.multiEvents),
		notificationActions: slices.Clone(s.notificationActions), notificationEvents: slices.Clone(s.notificationEvents), completed: s.Completed, sessionCompleted: s.sessionCompleted, projectedRoots: s.projectedRoots,
		foreignModifications: len(s.foreignTablesOrZero().modifications)}
	c.holdPromotions = slices.Clone(s.foreignTablesOrZero().holdPromotions)
	if s.CatalogAuthority != nil {
		owned := *s.CatalogAuthority
		owned.Observations = slices.Clone(owned.Observations)
		c.catalogAuthority = &owned
	}
	if s.truncateSlot != nil {
		owned := *s.truncateSlot
		owned.Relations, owned.Sequences, owned.Names = slices.Clone(owned.Relations), slices.Clone(owned.Sequences), slices.Clone(owned.Names)
		owned.Selected = maps.Clone(owned.Selected)
		c.truncate = &owned
	}
	if s.DropsSlot != nil {
		c.drops = s.DropsSlot.CloneSlot()
	}
	if s.ForeignKeysSlot != nil {
		owned := *s.ForeignKeysSlot
		owned.KeyTargets, owned.AlterKeys = slices.Clone(owned.KeyTargets), slices.Clone(owned.AlterKeys)
		c.foreignKeys = &owned
	}
	if s.viewCreationSlot != nil {
		owned := *s.viewCreationSlot
		owned.Active = slices.Clone(owned.Active)
		if owned.Changes != nil {
			owned.Changes = owned.Changes.Clone()
		}
		c.viewCreation = &owned
	}
	if s.materializedSlot != nil {
		owned := *s.materializedSlot
		owned.Sources = slices.Clone(owned.Sources)
		c.materialized = &owned
	}
	// A journal is COW at command boundaries. Fence an already-live statement
	// journal as well, so a nested utility cannot mutate its retained predecessor.
	if s.Catalog != nil {
		s.Catalog = s.Catalog.Clone()
	}
	t.Mu.Lock()
	t.Savepoints = append(t.Savepoints, mark)
	t.Mu.Unlock()
	return c, nil
}

func (s *StatementScope) endPLException(c *plExceptionCheckpoint, rollback bool, primary error) error {
	if c == nil || c.scope != s || c.owner != s.Owner || c.disposed {
		return errors.New("stuntgres: foreign or disposed exception checkpoint")
	}
	t := s.Owner
	t.Mu.Lock()
	n := len(t.Savepoints) - 1
	if n < 0 || !t.Savepoints[n].Internal || t.Savepoints[n].Serial != c.serial {
		t.Mu.Unlock()
		return errors.New("stuntgres: exception checkpoints are not LIFO")
	}
	mark := t.Savepoints[n]
	t.Mu.Unlock()
	c.disposed = true
	s.foreignUnlocked()
	var cleanup error
	if rollback {
		// Modification resources belong to the failed suffix. Participant
		// statements stay enrolled: rollback/release their unnamed provider mark,
		// not the root statement or an unrelated live producer.
		if slot := s.foreignTablesSlot; slot != nil {
			for _, modification := range slot.modifications[c.foreignModifications:] {
				cleanup = errors.Join(cleanup, modification.End(context.WithoutCancel(s.Op.Context())))
			}
			clear(slot.modifications[c.foreignModifications:])
			slot.modifications = slot.modifications[:c.foreignModifications]
		}
		cleanup = errors.Join(cleanup, foreignSavepoint(t, context.WithoutCancel(s.Op.Context()), 1, mark.Serial))
	}
	if cleanup == nil {
		cleanup = foreignSavepoint(t, context.WithoutCancel(s.Op.Context()), 2, mark.Serial)
	}
	t.Mu.Lock()
	if rollback {
		restoreSavepoint(t, mark, mark.Settings.Clone())
		s.Stage.Rows, s.Stage.Deletes, s.Stage.replacing = c.rows, c.deletes, c.replacing
		s.OwnWrites, s.OwnDeletes = c.ownWrites, c.ownDeletes
		s.Catalog, s.CatalogAuthority = c.catalog, c.catalogAuthority
		if c.foreignActions != nil && s.foreignActionsSlot != nil {
			*s.foreignActionsSlot = *c.foreignActions
		} else {
			s.foreignActionsSlot = c.foreignActions
		}
		if c.indexes != nil && s.indexesSlot != nil {
			*s.indexesSlot = *c.indexes
		} else {
			s.indexesSlot = c.indexes
		}
		s.truncateSlot, s.DropsSlot, s.ForeignKeysSlot = c.truncate, c.drops, c.foreignKeys
		s.viewCreationSlot, s.materializedSlot = c.viewCreation, c.materialized
		if s.foreignTablesSlot != nil {
			s.foreignTablesSlot.holdPromotions = c.holdPromotions
		}
		s.multiEvents = c.multiEvents
		s.notificationActions, s.notificationEvents = c.notificationActions, c.notificationEvents
		s.Completed, s.sessionCompleted, s.projectedRoots = c.completed, c.sessionCompleted, c.projectedRoots
	}
	t.TruncateSavepoints(n)
	t.Mu.Unlock()
	if rollback {
		s.Engine().Locks.Restore(t.Ticket, mark.Locks)
	}
	if cleanup != nil {
		return &proc.PLExceptionCleanupError{Primary: primary, Cleanup: cleanup}
	}
	return nil
}
