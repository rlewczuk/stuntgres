package engine

import (
	"stuntgres/internal/parser"
)

// The logical binder has already checked arity, expressions and types. Retain
// its DEFAULT markers and supply the real base's complete omission schedule.
func (e *Engine) lowerViewInsert(b *binding, auth AuthorizationView, s *parser.Insert) error {
	d := b.viewWrite.base
	b.defaults = make([]BoundExpr, len(d.columns))
	b.identityDiscard = nil
	b.insertProjection = nil
	for i, attr := range d.attributes {
		b.defaults[i] = d.defaults[attr].expression
		if identity, ok := d.identities[attr]; ok {
			b.defaults[i] = sequenceCallExpression{expressionInfo: expressionInfo{Usage: d.columns[i].Usage, Property: ExprProperties{Volatility: Volatile}}, name: "nextval", ref: e.domain.Ref(auth.catalog.Epoch(), identity.sequence), identity: true}
		}
	}
	if s.Override == parser.OverrideUser && len(d.identities) != 0 {
		b.identityDiscard = make([]bool, len(d.columns))
		for i, attr := range d.attributes {
			_, b.identityDiscard[i] = d.identities[attr]
		}
	}
	for i, slot := range b.targets {
		attr := d.attributes[slot]
		supplied := false
		defaultUsed := false
		if b.query != nil {
			supplied = true
		} else {
			for _, row := range b.expressions {
				if i < len(row) && row[i] != nil {
					supplied = true
				} else {
					defaultUsed = true
				}
			}
		}
		if supplied {
			if _, generated := d.generations[attr]; generated {
				return generationWriteError(d.columns[slot].Name, false)
			}
			if identity, ok := d.identities[attr]; ok && identity.mode == parser.IdentityAlways && s.Override == parser.OverrideNone {
				return identityInsertError(d.columns[slot].Name)
			}
		}
		discard := len(b.identityDiscard) != 0 && b.identityDiscard[slot]
		partial := len(b.arrayTargets) != 0 && b.arrayTargets[i] != nil
		if partial || supplied && !defaultUsed && !discard {
			b.defaults[slot] = nil
		}
	}
	if len(d.identities) != 0 {
		b.insertProjection = make([]int, len(d.columns))
		for i := range b.insertProjection {
			b.insertProjection[i] = -1
		}
		for i, slot := range b.targets {
			b.insertProjection[slot] = i
		}
	}
	return nil
}

// physicalBinding is used only after logical and per-layer permission checks.
// RETURNING keeps its analyzed logical schema, even when the target is hidden
// from the view after an UPDATE or was never visible after an INSERT.
func (b binding) physicalBinding() binding {
	if b.viewWrite == nil {
		return b
	}
	b.relation, b.ref = b.viewWrite.base, b.viewWrite.ref
	return b
}
