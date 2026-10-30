package storage

import "time"

// RelationMetadata is the canonical immutable ANALYZE publication for every
// relation kind. Store.RelationMetadata owns these records independently of
// physical heaps: a partitioned parent never acquires a synthetic Relation.
// Engine.mu guards root/map replacement; readers retain immutable records.
//
// Column Statistics may be transaction-private through the existing Journal;
// successful maintenance cardinality/count/time follows its separate runtime
// publication authority. Neither publication replaces physical row roots.
type RelationMetadata struct {
	Statistics       *RelationStatistics
	Imported         *ImportedRelationStatistics
	ForeignAnalyze   *ForeignAnalyzeStatistics
	Scanned          bool
	Tuples           int64
	InheritedScanned bool
	InheritedTuples  int64
	AnalyzeCount     int64
	LastAnalyze      time.Time
}

// ForeignAnalyzeStatistics keeps actual sample size separate from provider
// population/page estimates. It never creates a heap or exact tuple counter.
type ForeignAnalyzeStatistics struct {
	SampleRows                           uint64
	TotalRows, TotalDeadRows, TotalPages float64
}
