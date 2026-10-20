package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
)

// The logical binder has checked arity, expressions and types. Logical view
// defaults win for exposed columns; only true absence falls back to the base.
func (e *Engine) lowerViewInsert(b *binding, auth AuthorizationView, s *parser.Insert) error {
	d := viewWriteOf(b.viewWrite).base
	physical := binding{relation: d, ref: viewWriteOf(b.viewWrite).ref}
	domainScheduling := bindInsertDefaults(e.catalog.owner(), &physical, auth, parser.OverrideNone)
	b.insert.defaults, b.insert.defaultOrigins = physical.insert.defaults, physical.insert.defaultOrigins
	if b.insert.defaults == nil {
		b.insert.defaults = make([]expr.Expr, len(d.Columns))
	}
	b.insert.identityDiscard = nil
	b.insert.projection = nil
	for i, output := range viewWriteOf(b.viewWrite).outputs {
		direct, ok := viewDirectColumn(output)
		if !ok {
			continue
		}
		node, origin := columnDefault(auth.Catalog, viewWriteOf(b.viewWrite).logical.relation, i)
		if origin.definition.Expression != nil {
			if b.insert.defaultOrigins == nil {
				b.insert.defaultOrigins = make([]defaultOrigin, len(d.Columns))
				for j := range d.Columns {
					b.insert.defaultOrigins[j] = bindingDefaultOrigin(&physical, j)
				}
			}
			b.insert.defaults[direct.Slot] = defaultDestination(node, d.Columns[direct.Slot].Usage)
			b.insert.defaultOrigins[direct.Slot] = origin
		}
	}
	if s.Override == parser.OverrideUser && len(d.ColumnMeta.Identities) != 0 {
		b.insert.identityDiscard = make([]bool, len(d.Columns))
		for i, attr := range d.Attributes {
			_, b.insert.identityDiscard[i] = d.ColumnMeta.Identities[attr]
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
				return sequence.IdentityInsertError(d.Columns[slot].Name)
			}
		}
		discard := len(b.insert.identityDiscard) != 0 && b.insert.identityDiscard[slot]
		partial := len(b.arrayTargets) != 0 && b.arrayTargets[i] != nil
		if partial || supplied && !defaultUsed && !discard {
			b.insert.defaults[slot] = nil
		}
	}
	needsProjection := len(d.ColumnMeta.Identities) != 0
	if domainScheduling && !needsProjection {
		for _, node := range b.insert.defaults {
			if node != nil {
				needsProjection = true
				break
			}
		}
	}
	if needsProjection {
		b.insert.projection = make([]int, len(d.Columns))
		for i := range b.insert.projection {
			b.insert.projection[i] = -1
		}
		for i, slot := range b.targets {
			b.insert.projection[slot] = i
		}
	}
	return nil
}

// physicalBinding is used only after logical and per-layer permission checks.
// RETURNING keeps its analyzed logical schema, even when the target is hidden
// from the view after an UPDATE or was never visible after an INSERT.
func physicalBinding(b binding) binding {
	if b.viewWrite == nil {
		return b
	}
	b.relation, b.ref = viewWriteOf(b.viewWrite).base, viewWriteOf(b.viewWrite).ref
	return b
}
