package storage

const (
 RelationPages uint8 = 1 << iota
 RelationTuples
 RelationAllVisible
 RelationAllFrozen
 RelationStatisticsAll = RelationPages | RelationTuples | RelationAllVisible | RelationAllFrozen
)

// ImportedRelationStatistics is immutable once published. Presence distinguishes
// an explicit default from an estimate supplied by ordinary maintenance.
type ImportedRelationStatistics struct {
 Mask uint8
 Pages int32
 Tuples float32
 AllVisible int32
 AllFrozen int32
}

func MergeImportedRelationStatistics(old *ImportedRelationStatistics, delta ImportedRelationStatistics) *ImportedRelationStatistics {
 if delta.Mask == 0 { return old }
 var next ImportedRelationStatistics
 if old != nil { next = *old }
 if delta.Mask & RelationPages != 0 { next.Pages = delta.Pages }
 if delta.Mask & RelationTuples != 0 { next.Tuples = delta.Tuples }
 if delta.Mask & RelationAllVisible != 0 { next.AllVisible = delta.AllVisible }
 if delta.Mask & RelationAllFrozen != 0 { next.AllFrozen = delta.AllFrozen }
 next.Mask |= delta.Mask
 return &next
}

// WithoutImportedRelationStatistics preserves fields not replaced by maintenance.
func WithoutImportedRelationStatistics(old *ImportedRelationStatistics, mask uint8) *ImportedRelationStatistics {
 if old == nil || old.Mask & mask == 0 { return old }
 next := *old
 next.Mask &^= mask
 if next.Mask == 0 { return nil }
 return &next
}
