package core

import (
	"cmp"
	"slices"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
)

// Called under Engine.Mu at the real row-event boundary. The cached slice is
// immutable once an event retains it. Catalog or effective-role changes build
// a new slice; ordinary enabled events retain nil and allocate nothing.
func (s *StatementScope) foreignTriggerSuppression(ref ids.ObjectRef) []dml.ForeignTriggerSuppression {
	read := s.Engine().PrivateCatalogLocked(s.PendingCatalog())
	settings := s.Op.EffectiveSettings()
	settings.Mu.Lock()
	replica := settings.TypedValue("session_replication_role").Text == "replica"
	settings.Mu.Unlock()
	cached := &s.effectAdmission
	if cached.ref != ref || cached.catalog != read.State() {
		*cached = foreignEffectAdmission{ref: ref, catalog: read.State(), relevant: true}
	}
	if cached.modesCaptured && cached.replica == replica {
		return cached.suppressed
	}
	var suppressed []dml.ForeignTriggerSuppression
	for _, child := range read.Relations() {
		for _, key := range child.Constraints.ForeignKeys {
			if key.NotEnforced {
				continue
			}
			var mask uint8
			if key.Target == ref.Address {
				for slot := range 2 {
					if !key.TriggerMode(slot).Fires(replica) {
						mask |= 1 << slot
					}
				}
			}
			if child.Address() == ref.Address {
				for slot := 2; slot < 4; slot++ {
					if !key.TriggerMode(slot).Fires(replica) {
						mask |= 1 << slot
					}
				}
			}
			if mask != 0 {
				suppressed = append(suppressed, dml.ForeignTriggerSuppression{Constraint: key.OID, Slots: mask})
			}
		}
	}
	slices.SortFunc(suppressed, func(a, b dml.ForeignTriggerSuppression) int {
		return cmp.Compare(a.Constraint, b.Constraint)
	})
	cached.modesCaptured, cached.replica, cached.suppressed = true, replica, suppressed
	return suppressed
}
