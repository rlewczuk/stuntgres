package engine

import "context"

// The registry remains the sole cursor owner. This completion-owned vector only
// identifies newly promoted cursors whose old transaction failed publication;
// preexisting committed HOLD cursors and successful old commits are untouched.
func (s *StatementScope) discardForeignHoldPromotions(primary error) error {
	promotions := s.foreignTablesOrZero().holdPromotions
	s.foreignTablesSlot.clearHoldPromotions()
	if len(promotions) == 0 {
		return primary
	}
	s.foreignUnlocked()
	registry := s.op.session.cursors
	for _, promoted := range promotions {
		for name, current := range registry.entries {
			if current != promoted {
				continue
			}
			delete(registry.entries, name)
			// releaseProducer already ended the source before promotion. Dispose
			// drops owned materialized values and cannot replay that source End.
			if err := promoted.dispose(context.WithoutCancel(s.op.Context())); primary == nil {
				primary = err
			}
			break
		}
	}
	return primary
}
