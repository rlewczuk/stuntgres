package storage

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// StatisticsKey uses stable catalog identity, never a physical row slot.
type StatisticsKey struct {
	Attribute int32
	Inherited bool
}

type StatisticMCV struct {
	Value sqltype.Value
	Count uint64
}

// StatisticsHierarchy retains one shared immutable descriptor-layout capture.
// Runtime validates closure membership and stable attribute/type/collation
// layouts, not display names or descriptor pointers, before inherited use.
type StatisticsHierarchy struct {
	Relations []*catalog.Relation
	Session   ids.SessionToken // Captured TEMP-namespace visibility authority.
}

// ColumnStatistics is immutable after collection. Counts describe actual input
// rows: all visible rows for heaps, genuine acquired sample rows for foreign
// tables. Bounded MCV/histogram values summarize that input, never fake rows.
// DistinctKnown distinguishes unavailable equality from measured zero.
type ColumnStatistics struct {
	Usage                          sqltype.Usage
	Rows, NullCount, DistinctCount uint64
	DistinctKnown                  bool
	// DistinctOverride is a signed planner estimate, never a measured count.
	// Zero uses the ordinary measured encoding.
	DistinctOverride float64
	AverageWidth     int32
	MCV              []StatisticMCV
	Histogram        []sqltype.Value
	Hierarchy        *StatisticsHierarchy
}

type RelationStatistics struct {
	Columns  map[StatisticsKey]*ColumnStatistics
	Extended map[ExtendedStatisticsKey]*ExtendedStatistics
}

func (s *ColumnStatistics) NullFraction() float64 {
	if s == nil || s.Rows == 0 {
		return 0
	}
	return float64(s.NullCount) / float64(s.Rows)
}

// EncodedDistinct is PostgreSQL-style encoding, not the raw distinct count.
func (s *ColumnStatistics) EncodedDistinct() (float64, bool) {
	if s == nil || !s.DistinctKnown {
		return 0, false
	}
	if s.DistinctOverride != 0 {
		return s.DistinctOverride, true
	}
	if s.Rows == 0 || s.DistinctCount == 0 {
		return 0, true
	}
	if s.DistinctCount == s.Rows && s.NullCount == 0 {
		return -1, true
	}
	if float64(s.DistinctCount) > float64(s.Rows)/10 {
		return -float64(s.DistinctCount) / float64(s.Rows), true
	}
	return float64(s.DistinctCount), true
}

// MergeStatistics applies selected immutable entries to the current root. A nil
// replacement deletes an entry; unselected column payloads retain their backing.
func MergeStatistics(base *RelationStatistics, replacements map[StatisticsKey]*ColumnStatistics) *RelationStatistics {
	if len(replacements) == 0 {
		return base
	}
	n := 0
	if base != nil {
		n = len(base.Columns)
	}
	columns := make(map[StatisticsKey]*ColumnStatistics, n+len(replacements))
	if base != nil {
		for key, value := range base.Columns {
			columns[key] = value
		}
	}
	for key, value := range replacements {
		if value == nil {
			delete(columns, key)
		} else {
			columns[key] = value
		}
	}
	if len(columns) == 0 && len(extendedStatistics(base)) == 0 {
		return nil
	}
	return &RelationStatistics{Columns: columns, Extended: extendedStatistics(base)}
}

// CompatibleStatistics preserves stale facts only under the same stable
// attribute, concrete type, typmod and collation authority.
func CompatibleStatistics(stats *RelationStatistics, relation *catalog.Relation) *RelationStatistics {
	if stats == nil {
		return nil
	}
	var deletions map[StatisticsKey]*ColumnStatistics
	for key, value := range stats.Columns {
		compatible := false
		if relation != nil && value != nil {
			for slot, attribute := range relation.Attributes {
				if attribute == key.Attribute && slot < len(relation.Columns) && relation.Columns[slot].Usage == value.Usage {
					compatible = true
					break
				}
			}
		}
		if !compatible {
			if deletions == nil {
				deletions = make(map[StatisticsKey]*ColumnStatistics)
			}
			deletions[key] = nil
		}
	}
	return MergeStatistics(stats, deletions)
}
