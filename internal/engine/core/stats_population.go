package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
)

// RecordStatsPopulation captures policy at bulk staging, without changing the
// existing physical utility publication route. Admission owns this scratch until
// the existing overlay accepts it or a direct utility publication completes.
func (s *StatementScope) RecordStatsPopulation(ref ids.ObjectRef, inserts, deletes int64, replacement bool) {
	_, _, policy := s.Op.EffectiveSettings().StatsConfiguration()
	if !policy.Counts || !replacement && inserts == 0 && deletes == 0 {
		return
	}
	key := statistics.Key{Object: ref, Category: statistics.Relation}
	event := statistics.Fact{Inserts: inserts, Deletes: deletes, Live: inserts - deletes, Dead: deletes, Modifications: inserts + deletes, InsertsSince: inserts, Truncated: replacement}
	s.statsPopulation.Record(key, event, len(s.Owner.Savepoints))
}
func (s *StatementScope) absorbStatsPopulation() {
	s.Stage.Stats.Take(&s.statsPopulation)
}

// CompleteStatsPopulation is called only after existing physical publication
// succeeds. Overlay acceptance already transferred its facts to the write ledger;
// direct utility completion releases settled facts, never recollects policy.
func (s *StatementScope) CompleteStatsPopulation() {
	settleStatsLedger(s.Engine(), s.Op.Session, &s.statsPopulation, true)
}
func (s *StatementScope) AbortStatsPopulation() {
	s.Stage.Stats.Take(&s.statsPopulation)
}
