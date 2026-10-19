package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// The logical binder has checked arity, expressions and types. Logical view
// defaults win for exposed columns; only true absence falls back to the base.
func (e *Engine) lowerViewInsert(b *binding, auth AuthorizationView, s *parser.Insert) error {
	d := b.viewWrite.base
	physical := binding{relation: d, ref: b.viewWrite.ref}
	domainScheduling := e.bindInsertDefaults(&physical, auth, parser.OverrideNone)
	b.defaults, b.defaultOrigins = physical.defaults, physical.defaultOrigins
	if b.defaults == nil {
		b.defaults = make([]expr.Expr, len(d.Columns))
	}
	b.identityDiscard = nil
	b.insertProjection = nil
	for i, output := range b.viewWrite.outputs {
		direct, ok := viewDirectColumn(output)
		if !ok {
			continue
		}
		node, origin := columnDefault(auth.Catalog, b.viewWrite.logical.relation, i)
		if origin.definition.Expression != nil {
			if b.defaultOrigins == nil {
				b.defaultOrigins = make([]defaultOrigin, len(d.Columns))
				for j := range d.Columns {
					b.defaultOrigins[j] = physical.defaultOriginAt(j)
				}
			}
			b.defaults[direct.slot] = defaultDestination(node, d.Columns[direct.slot].Usage)
			b.defaultOrigins[direct.slot] = origin
		}
	}
	if s.Override == parser.OverrideUser && len(d.ColumnMeta.Identities) != 0 {
		b.identityDiscard = make([]bool, len(d.Columns))
		for i, attr := range d.Attributes {
			_, b.identityDiscard[i] = d.ColumnMeta.Identities[attr]
		}
	}
	for i, slot := range b.targets {
		attr := d.Attributes[slot]
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
			if _, generated := d.ColumnMeta.Generations[attr]; generated {
				return generationWriteError(d.Columns[slot].Name, false)
			}
			if identity, ok := d.ColumnMeta.Identities[attr]; ok && identity.Mode == parser.IdentityAlways && s.Override == parser.OverrideNone {
				return identityInsertError(d.Columns[slot].Name)
			}
		}
		discard := len(b.identityDiscard) != 0 && b.identityDiscard[slot]
		partial := len(b.arrayTargets) != 0 && b.arrayTargets[i] != nil
		if partial || supplied && !defaultUsed && !discard {
			b.defaults[slot] = nil
		}
	}
	needsProjection := len(d.ColumnMeta.Identities) != 0
	if domainScheduling && !needsProjection {
		for _, node := range b.defaults {
			if node != nil {
				needsProjection = true
				break
			}
		}
	}
	if needsProjection {
		b.insertProjection = make([]int, len(d.Columns))
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
