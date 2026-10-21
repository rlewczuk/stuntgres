package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
)

// The logical binder has checked arity, expressions and types. Logical view
// defaults win for exposed columns; only true absence falls back to the base.
func lowerViewInsert(owner *catalog.Owner, view catalog.Read, b *analyze.Binding, s *parser.Insert) error {
	d := viewWriteOf(b.ViewWrite).base
	physical := analyze.Binding{Relation: d, Ref: viewWriteOf(b.ViewWrite).ref}
	domainScheduling := analyze.BindInsertDefaults(owner, &physical, view, parser.OverrideNone)
	b.Insert.Defaults, b.Insert.DefaultOrigins = physical.Insert.Defaults, physical.Insert.DefaultOrigins
	if b.Insert.Defaults == nil {
		b.Insert.Defaults = make([]expr.Expr, len(d.Columns))
	}
	b.Insert.IdentityDiscard = nil
	b.Insert.Projection = nil
	for i, output := range viewWriteOf(b.ViewWrite).outputs {
		direct, ok := ViewDirectColumn(output)
		if !ok {
			continue
		}
		node, origin := analyze.ColumnDefault(view, viewWriteOf(b.ViewWrite).logical.Relation, i)
		if origin.Definition.Expression != nil {
			if b.Insert.DefaultOrigins == nil {
				b.Insert.DefaultOrigins = make([]analyze.DefaultOrigin, len(d.Columns))
				for j := range d.Columns {
					b.Insert.DefaultOrigins[j] = bindingDefaultOrigin(&physical, j)
				}
			}
			b.Insert.Defaults[direct.Slot] = analyze.DefaultDestination(node, d.Columns[direct.Slot].Usage)
			b.Insert.DefaultOrigins[direct.Slot] = origin
		}
	}
	if s.Override == parser.OverrideUser && len(d.ColumnMeta.Identities) != 0 {
		b.Insert.IdentityDiscard = make([]bool, len(d.Columns))
		for i, attr := range d.Attributes {
			_, b.Insert.IdentityDiscard[i] = d.ColumnMeta.Identities[attr]
		}
	}
	for i, slot := range b.Targets {
		attr := d.Attributes[slot]
		supplied := false
		defaultUsed := false
		if b.Query != nil {
			supplied = true
		} else {
			for _, row := range b.Expressions {
				if i < len(row) && row[i] != nil {
					supplied = true
				} else {
					defaultUsed = true
				}
			}
		}
		if supplied {
			if _, generated := d.ColumnMeta.Generations[attr]; generated {
				return analyze.GenerationWriteError(d.Columns[slot].Name, false)
			}
			if identity, ok := d.ColumnMeta.Identities[attr]; ok && identity.Mode == parser.IdentityAlways && s.Override == parser.OverrideNone {
				return sequence.IdentityInsertError(d.Columns[slot].Name)
			}
		}
		discard := len(b.Insert.IdentityDiscard) != 0 && b.Insert.IdentityDiscard[slot]
		partial := len(b.ArrayTargets) != 0 && b.ArrayTargets[i] != nil
		if partial || supplied && !defaultUsed && !discard {
			b.Insert.Defaults[slot] = nil
		}
	}
	needsProjection := len(d.ColumnMeta.Identities) != 0
	if domainScheduling && !needsProjection {
		for _, node := range b.Insert.Defaults {
			if node != nil {
				needsProjection = true
				break
			}
		}
	}
	if needsProjection {
		b.Insert.Projection = make([]int, len(d.Columns))
		for i := range b.Insert.Projection {
			b.Insert.Projection[i] = -1
		}
		for i, slot := range b.Targets {
			b.Insert.Projection[slot] = i
		}
	}
	return nil
}

// physicalBinding is used only after logical and per-layer permission checks.
// RETURNING keeps its analyzed logical schema, even when the target is hidden
// from the view after an UPDATE or was never visible after an INSERT.
func physicalBinding(b analyze.Binding) analyze.Binding {
	if b.ViewWrite == nil {
		return b
	}
	b.Relation, b.Ref = viewWriteOf(b.ViewWrite).base, viewWriteOf(b.ViewWrite).ref
	return b
}
