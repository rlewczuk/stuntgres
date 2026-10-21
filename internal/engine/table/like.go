package table

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
)

// CaptureLikeSource records source once, so publication can check that it
// did not change.
func (s *Stage) CaptureLikeSource(source catalog.Descriptor) {
	if !slices.Contains(s.LikeSources, source) {
		s.LikeSources = append(s.LikeSources, source)
	}
}

// ValidateLikeSources checks the captured LIKE sources: an incumbent of base
// must be unchanged in current, a source the stage created itself must keep
// its staged incarnation. A nil stage has no sources.
func (s *Stage) ValidateLikeSources(base catalog.Read, staged ddl.Stage, current catalog.Read) error {
	if s == nil {
		return nil
	}
	sources := s.LikeSources
	if len(sources) == 0 {
		return nil
	}
	if current.Epoch() != base.Epoch() {
		return ids.ErrCatalogReplaced
	}
	for _, source := range sources {
		address := source.Address()
		if original, incumbent := base.Find(address); incumbent {
			if original != source || current.Get(address) != source {
				return ids.ErrCatalogReplaced
			}
		} else {
			stagedRelation, ok := staged.Get(address).(*catalog.Relation)
			captured, sourceOK := source.(*catalog.Relation)
			// Private index-history normalization may replace the pointer while
			// retaining the staged incarnation and definition revision.
			if !ok || !sourceOK || stagedRelation.Revision() != source.Revision() || stagedRelation.RelationKind != captured.RelationKind {
				return fmt.Errorf("stuntgres: invalid staged LIKE source")
			}
		}
	}
	return nil
}
