package stored

import "stuntgres/internal/engine/catalog"

// Both public deparse and portable replay spell the declared properties, never
// the transaction's SET CONSTRAINTS state.
func triggerConstraintTimingSQL(c catalog.TriggerConstraint) string {
	text := " NOT DEFERRABLE"
	if c.Deferrable {
		text = " DEFERRABLE"
	}
	if c.InitiallyDeferred {
		return text + " INITIALLY DEFERRED"
	}
	return text + " INITIALLY IMMEDIATE"
}
