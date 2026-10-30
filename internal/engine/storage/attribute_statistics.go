package storage

import (
 "fmt"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

// ImportedStatistics preserves probability facts without inventing a measured
// population. Payloads and all occupied slots are immutable after publication.
type ImportedStatistics struct {
 NullFractionSet, DistinctSet bool
 NullFraction, Distinct float32
 Slots [5]AttributeStatisticsSlot
}

type AttributeStatisticsSlot struct {
 Kind int16
 Values sqltype.Value
 Numbers sqltype.Value
}

func (s *ImportedStatistics) Slot(kind int16) (AttributeStatisticsSlot, bool) {
 if s != nil { for _, slot := range s.Slots { if slot.Kind == kind { return slot, true } } }
 return AttributeStatisticsSlot{}, false
}

func (s *ImportedStatistics) SetSlot(slot AttributeStatisticsSlot) error {
 empty := -1
 for i, old := range s.Slots {
  if old.Kind == slot.Kind { s.Slots[i] = slot; return nil }
  if old.Kind == 0 && empty < 0 { empty = i }
 }
 if empty < 0 { return &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"XX000", Message:"maximum number of statistics slots exceeded: 6"} }
 if slot.Kind < 1 || slot.Kind > 7 { return fmt.Errorf("stuntgres: invalid statistics kind %d", slot.Kind) }
 s.Slots[empty] = slot
 return nil
}

// AttributeStatisticsCopy is the only writable construction copy. Measured
// counts and typed immutable backing remain unchanged, including on rollback.
func AttributeStatisticsCopy(old *ColumnStatistics, usage sqltype.Usage) *ColumnStatistics {
 next := &ColumnStatistics{Usage:usage}
 imported := &ImportedStatistics{}
 if old != nil { *next = *old; if old.Imported != nil { *imported = *old.Imported } } else {
  imported.NullFractionSet, imported.DistinctSet = true, true
 }
 next.Imported = imported
 return next
}

// MCVProbability presents the effective facts to the probability planner path.
func (s *ColumnStatistics) MCVLen() int {
 if slot, ok := s.Imported.Slot(1); ok { return slot.Values.ArrayLen() }
 return len(s.MCV)
}
func (s *ColumnStatistics) MCVProbability(i int) (sqltype.Value, float64) {
 if slot, ok := s.Imported.Slot(1); ok { n, _ := slot.Numbers.ArrayElement(i).Float(); return slot.Values.ArrayElement(i), n }
 return s.MCV[i].Value, float64(s.MCV[i].Count)/float64(s.Rows)
}
func (s *ColumnStatistics) HistogramLen() int {
 if slot, ok := s.Imported.Slot(2); ok { return slot.Values.ArrayLen() }
 return len(s.Histogram)
}
func (s *ColumnStatistics) HistogramValue(i int) sqltype.Value {
 if slot, ok := s.Imported.Slot(2); ok { return slot.Values.ArrayElement(i) }
 return s.Histogram[i]
}
