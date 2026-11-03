package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
)

// recordSequencePageAccess records a successful generator encounter, not a
// returned sequence value. Engine.Mu and physical-session admission are held;
// the provider mutex is a leaf. Existing-object encounters survive rollback.
func (s *StatementScope) recordSequencePageAccess(ref ids.ObjectRef) {
	_, _, policy := s.Op.EffectiveSettings().StatsConfiguration()
	if !policy.Counts {
		return
	}
	PayloadOf(s.Op.Session).statsWriter.Add(&s.Engine().Statistics,
		statistics.Key{Object: ref, Category: statistics.Relation},
		statistics.Fact{SequencePageAccesses: 1})
}
