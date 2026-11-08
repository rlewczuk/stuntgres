package catalog

import "stuntgres/internal/engine/ids"

// Trigger comments are relation-owned metadata, not descriptor comments. A
// stored entry is meaningful only while the OID still addresses a live
// pg_trigger row on that relation: a payload trigger or an internal
// foreign-key trigger fact. Reads filter through this liveness rule, which is
// what makes a dropped trigger's entry inert; OIDs are never reused.

// liveTrigger reports whether oid is a live trigger of d.
func liveTrigger(view Read, d *Relation, oid ids.OID) bool {
	if d.Triggers != nil {
		for i := range d.Triggers.Len() {
			if d.Triggers.OID(i) == oid {
				return true
			}
		}
	}
	for fact := range ForeignKeyTriggerFacts(view) {
		if fact.OID == oid && fact.Relation == d.Address() {
			return true
		}
	}
	return false
}

// TriggerOIDByName resolves a live trigger name on d: payload triggers first,
// then internal foreign-key trigger facts. A validated payload cannot fail its
// visit, so the error channel only stops a malformed fixture from resolving.
func TriggerOIDByName(view Read, d *Relation, name string) (ids.OID, bool) {
	if d.Triggers != nil {
		var found ids.OID
		ok := false
		_ = d.Triggers.Visit(func(fact TriggerFact) error {
			if fact.Name == name {
				found, ok = fact.OID, true
			}
			return nil
		})
		if ok {
			return found, true
		}
	}
	for fact := range ForeignKeyTriggerFacts(view) {
		if fact.Relation == d.Address() && fact.Name == name {
			return fact.OID, true
		}
	}
	return 0, false
}

// TriggerCommentText returns the stored text of a live trigger comment.
func TriggerCommentText(view Read, oid ids.OID) (string, bool) {
	for _, d := range view.Relations() {
		text, ok := d.TriggerComments[oid]
		if !ok || text == "" || !liveTrigger(view, d, oid) {
			continue
		}
		return text, true
	}
	return "", false
}

// VisitTriggerComments visits every live stored trigger comment. The common
// uncommented catalog returns after the fast-path scan.
func VisitTriggerComments(view Read, visit func(oid ids.OID, text string) error) error {
	commented := false
	for _, d := range view.Relations() {
		if len(d.TriggerComments) != 0 {
			commented = true
			break
		}
	}
	if !commented {
		return nil
	}
	for _, d := range view.Relations() {
		for oid, text := range d.TriggerComments {
			if text == "" || !liveTrigger(view, d, oid) {
				continue
			}
			if err := visit(oid, text); err != nil {
				return err
			}
		}
	}
	return nil
}