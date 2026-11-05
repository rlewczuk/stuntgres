package core

import (
	"context"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

// Events are delivered once at their actual owner, inclusively to active
// lexical ancestors. Child completion never merges a vector a second time.
func (s *StatementScope) recordStatementCounters(v statistics.StatementCounters) {
	f := s.statementStats
	if f == nil {
		f = s.statementStatsParent
	}
	for ; f != nil; f = f.parent {
		if f.finished {
			continue
		}
		if !f.planStart.IsZero() {
			f.planCounters.Add(v)
		} else if f.utility || f.planAccepted {
			f.execCounters.Add(v)
		}
	}
}

// Retained executors borrow only the currently active enclosing sink. Never
// retain lexical ancestry across suspension or producer transfer.
func (s *StatementScope) activateStatementCounters(f *statementStatsFrame) *statementStatsFrame {
	previous := s.statementStats
	if f != nil && f != previous {
		f.parent = previous
		if f.parent == nil {
			f.parent = s.statementStatsParent
		}
		s.statementStats = f
	}
	return previous
}

func (s *StatementScope) restoreStatementCounters(f, previous *statementStatsFrame) {
	if f != nil && f != previous {
		f.parent = nil
		s.statementStats = previous
	}
}

func statementMutationImage(ctx context.Context, old, next *storage.RowVersion) (uint64, error) {
	var image uint64
	for _, row := range [2]*storage.RowVersion{old, next} {
		if row != nil {
			n, err := storage.RowAllocation(ctx, row.Values)
			if err != nil {
				return 0, err
			}
			image += uint64(n)
		}
	}
	return image, nil
}

func statementMutationCounters(read catalog.Read, relation *catalog.Relation, image uint64) statistics.StatementCounters {
	var v statistics.StatementCounters
	if image == 0 {
		return v
	}
	dirtied := int64(1 + (image-1)/8192)
	if table.EffectivePersistence(read, relation) == "temporary" {
		v.LocalDirtied = dirtied
	} else {
		v.SharedDirtied = dirtied
	}
	if relation.RelationKind == catalog.TableRelation && relation.Address().ObjectID >= firstNormalObjectID && table.EffectivePersistence(read, relation) == "logged" {
		v.WALRecords, v.WALBytes = 1, image
	}
	return v
}

// A partition UPDATE has two accepted physical sides but one logical image.
// Keep only the accepted OLD size and fallback vector across that lexical move;
// nested routine scratch saves this owner rather than borrowing it.
type statementStatsMove struct {
	destination ids.ObjectRef
	oldImage    uint64
	deletion    statistics.StatementCounters
}

func (h *statementHost) BeginUpdateMove(destination ids.ObjectRef) {
	h.scope().statementStatsMove = statementStatsMove{destination: destination}
}

func (h *statementHost) EndUpdateMove() {
	s := h.scope()
	s.recordStatementCounters(s.statementStatsMove.deletion)
	s.statementStatsMove = statementStatsMove{}
}

func (s *StatementScope) acceptedStatementDeletion(ref ids.ObjectRef, old storage.RowVersion, moved bool) error {
	if !moved || s.statementStatsMove.destination == (ids.ObjectRef{}) {
		return s.acceptedStatementMutation(ref, &old, nil)
	}
	if s.statementStats == nil && s.statementStatsParent == nil {
		return nil
	}
	image, err := statementMutationImage(s.Op.Context(), &old, nil)
	if err != nil {
		return err
	}
	read := s.Engine().PrivateCatalogLocked(s.PendingCatalog())
	relation, _ := read.Relation(ref.Address)
	s.statementStatsMove.oldImage = image
	s.statementStatsMove.deletion = statementMutationCounters(read, relation, image)
	return nil
}

func (s *StatementScope) recordStatementInsert(ref ids.ObjectRef, counters statistics.StatementCounters) {
	s.recordStatementCounters(counters)
	if s.statementStatsMove.destination == ref {
		s.statementStatsMove.oldImage = 0
		s.statementStatsMove.deletion = statistics.StatementCounters{}
	}
}

// Caller holds the canonical accepted mutation window, never a candidate or
// command-result count. RowAllocation reads immutable type/NULL shape only.
func (s *StatementScope) acceptedStatementMutation(ref ids.ObjectRef, old, next *storage.RowVersion) error {
	if s.statementStats == nil && s.statementStatsParent == nil {
		return nil
	}
	image, err := statementMutationImage(s.Op.Context(), old, next)
	if err != nil {
		return err
	}
	read := s.Engine().PrivateCatalogLocked(s.PendingCatalog())
	relation, _ := read.Relation(ref.Address)
	s.recordStatementCounters(statementMutationCounters(read, relation, image))
	return nil
}

// Size every accepted batch member before publishing any work. Per-image page
// rounding is retained even though the scalar vector is merged only once.
func (s *StatementScope) statementMutationBatch(ref ids.ObjectRef, old, next []storage.RowVersion) (statistics.StatementCounters, error) {
	var total statistics.StatementCounters
	if s.statementStats == nil && s.statementStatsParent == nil {
		return total, nil
	}
	read := s.Engine().PrivateCatalogLocked(s.PendingCatalog())
	relation, _ := read.Relation(ref.Address)
	for i := range next {
		var previous *storage.RowVersion
		if old != nil {
			previous = &old[i]
		}
		image, err := statementMutationImage(s.Op.Context(), previous, &next[i])
		if err != nil {
			return statistics.StatementCounters{}, err
		}
		if old == nil && len(next) == 1 && s.statementStatsMove.destination == ref {
			image += s.statementStatsMove.oldImage
		}
		total.Add(statementMutationCounters(read, relation, image))
	}
	return total, nil
}

func statementWALBytes(ctx context.Context, typ *sqltype.Descriptor, value uint64) (sqltype.Value, error) {
	return typ.Input(ctx, strconv.FormatUint(value, 10), -1)
}

func projectStatementCounters(ctx context.Context, result *sqltype.Descriptor, fields []sqltype.Value, c statistics.StatementCounters) error {
	for _, cell := range [...]struct{ index int; value int64 }{
		{18,c.SharedHit},{19,c.SharedRead},{20,c.SharedDirtied},{21,c.SharedWritten},
		{22,c.LocalHit},{23,c.LocalRead},{24,c.LocalDirtied},{25,c.LocalWritten},
		{26,c.TempRead},{27,c.TempWritten},{34,c.WALRecords},{35,c.WALFPI},
		{37,c.WALBuffersFull},{38,c.JITFunctions},{40,c.JITInliningCount},
		{42,c.JITOptimizationCount},{44,c.JITEmissionCount},{46,c.JITDeformCount},
		{48,c.WorkersPlanned},{49,c.WorkersLaunched},
	} {
		value, err := sqltype.Integer(result.CompositeMember(cell.index).Usage.Type, cell.value)
		if err != nil { return err }
		fields[cell.index] = value
	}
	for _, cell := range [...]struct{ index int; value float64 }{
		{28,c.SharedReadTime},{29,c.SharedWriteTime},{30,c.LocalReadTime},{31,c.LocalWriteTime},
		{32,c.TempReadTime},{33,c.TempWriteTime},{39,c.JITGenerationTime},{41,c.JITInliningTime},
		{43,c.JITOptimizationTime},{45,c.JITEmissionTime},{47,c.JITDeformTime},
	} {
		value, err := sqltype.Float(result.CompositeMember(cell.index).Usage.Type, cell.value)
		if err != nil { return err }
		fields[cell.index] = value
	}
	value, err := statementWALBytes(ctx, result.CompositeMember(36).Usage.Type, c.WALBytes)
	if err != nil { return err }
	fields[36] = value
	return nil
}
