package exec

import (
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/storage"
	"time"
)

// StatisticsHost is an optional execution capability so detached/runtime test
// sources do not need a live session. No host or writer is called per row.
type StatisticsHost interface {
	DatabaseStatisticsPolicy() statistics.Policy
	RecordDatabaseStatistics(statistics.DatabaseFact)
}

func (s *Source) prepareDatabaseStatistics() {
	if s.statsInitialized {
		return
	}
	s.statsInitialized = true
	if h, ok := s.LiveHost().(StatisticsHost); ok {
		policy := h.DatabaseStatisticsPolicy()
		s.statsCounts, s.statsTiming = policy.Counts, policy.IOTiming
	}
	for _, leaf := range s.Inheritance {
		leaf.Prefix.statsInitialized = true
		leaf.Prefix.statsCounts, leaf.Prefix.statsTiming = s.statsCounts, s.statsTiming
	}
}
func (s *Source) reportDatabaseStatistics() {
	if !s.statsWorker && s.databaseStatistics != (statistics.DatabaseFact{}) {
		if h, ok := s.LiveHost().(StatisticsHost); ok {
			h.RecordDatabaseStatistics(s.databaseStatistics)
		}
		s.databaseStatistics = statistics.DatabaseFact{}
	}
	s.statsInitialized = false
	s.statsCounts, s.statsTiming = false, false
}

// FlushDatabaseStatistics consumes only real executed demand, including retained
// cursor demand before its current FETCH authority is detached.
func (s *Source) FlushDatabaseStatistics() {
	if s == nil {
		return
	}
	s.reportDatabaseStatistics()
	for _, child := range s.Sources {
		if child != s {
			child.FlushDatabaseStatistics()
		}
	}
	s.Input.FlushDatabaseStatistics()
	s.NestedInputs.FlushDatabaseStatistics()
	for _, leaf := range s.Inheritance {
		leaf.Prefix.FlushDatabaseStatistics()
	}
}
func (s *Stage) databaseFetched(count int, indexed bool) {
	if !s.physicalScan || count <= 0 {
		return
	}
	source := s.Source
	source.prepareDatabaseStatistics()
	if !source.statsCounts {
		return
	}
	source.databaseStatistics.Returned += int64(count)
	source.databaseStatistics.BlocksHit += int64(count)
	if indexed {
		source.databaseStatistics.Fetched += int64(count)
	}
}
func (s *Stage) databaseOutput(ordinal int) {
	if s.physicalScan && ordinal >= s.statsFrontier {
		s.databaseFetched(1, false)
		s.statsFrontier = ordinal + 1
	}
}
func recordDatabaseWorkers(source *Source, planned, launched int) {
	if h, ok := source.LiveHost().(StatisticsHost); ok {
		h.RecordDatabaseStatistics(statistics.DatabaseFact{WorkersPlanned: int64(planned), WorkersLaunched: int64(launched)})
	}
}
func mergeDatabaseWorker(source *Source, fact statistics.DatabaseFact) {
	if h, ok := source.LiveHost().(StatisticsHost); ok {
		h.RecordDatabaseStatistics(fact)
	}
}

// Real local cell access timing, in contrast to tuple demand counts, includes
// repeated accesses made by expression evaluation. Clocks are never read off.
func (s *Source) startDatabaseRead() time.Time {
	if !s.statsCounts || !s.statsTiming {
		return time.Time{}
	}
	return time.Now()
}
func (s *Source) finishDatabaseRead(start time.Time) {
	if !start.IsZero() {
		s.databaseStatistics.ReadTime += time.Since(start)
	}
}

// ReadSequentialVersion is the physical target fetch used by DML scanners
// which do not pull an exec.Stage. Candidate replay uses VersionAt instead.
// Facts stay on the owning source and drain with its existing Close lifetime.
func (s *Source) ReadSequentialVersion(index int) storage.RowVersion {
	physical := s
	if s.Inheritance != nil {
		segment, ordinal := s.inheritanceSegment(index)
		physical, index = segment.Prefix, ordinal
	}
	if physical.View == nil || physical.Anonymous != nil || physical.Materialized || physical.Foreign != nil || physical.Singleton {
		return physical.VersionAt(physical.RawIndex(index))
	}
	s.prepareDatabaseStatistics()
	start := s.startDatabaseRead()
	version := physical.VersionAt(physical.RawIndex(index))
	s.finishDatabaseRead(start)
	if s.statsCounts {
		s.databaseStatistics.Returned++
		s.databaseStatistics.BlocksHit++
	}
	return version
}
