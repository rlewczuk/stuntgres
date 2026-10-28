package core

import (
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// CheckForeignKeySnapshot uses only the original stable supporting root. Current
// integrity has already missed, so a retained match is an isolation conflict, not
// permission to publish a reference to an obsolete parent version.
func (h *statementHost) CheckForeignKeySnapshot(ref ids.ObjectRef, job dml.ForeignKeyJob, child []sqltype.Value) error {
	s := h.scope()
	if !s.Owner.Isolation.Stable() || !s.Owner.SnapshotSet {
		return nil
	}
	e := s.Engine()
	e.Mu.Lock()
	retained, ok := s.Owner.Roots[ref.Address]
	capture, exists := retained.Indexes[job.Index.Address()]
	if !ok || !exists {
		e.Mu.Unlock()
		return nil
	}
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() || retained.Storage != e.PrivateStorageLocked(s.PendingCatalog(), ref.Address) || !index.SameRelationRowShape(retained.Relation, job.Parent) || !index.SameIndexKeySemantics(capture.Definition, job.Index) {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	rows := table.ForeignKeyRows{Storage: retained.Storage, Committed: retained.Versions, Ends: retained.Ends, Frontier: s.Owner.Frontier, Root: capture.Root}
	view := e.PrivateCatalogLocked(s.PendingCatalog())
	namespace, ok := view.Namespace(job.Parent.Namespace())
	e.Mu.Unlock()
	if !ok {
		return ids.ErrCatalogReplaced
	}
	input, _ := h.Environments()
	if err := table.PrepareForeignKeyChild(h.Context(), job.Comparisons, child, job.ChildKey, input); err != nil {
		return err
	}
	matching, _, err := table.FindForeignKeyParent(h.Context(), capture.Definition, job.Comparisons, rows, job.ChildKey, input)
	if err != nil || matching.Row == 0 {
		return err
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent update", Context: `SQL statement "` + dml.ForeignKeyCheckQuery(job, namespace.Name()) + `"`}
}
