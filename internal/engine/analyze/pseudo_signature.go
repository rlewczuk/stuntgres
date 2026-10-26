package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

type pseudoBinding struct {
	leaf sqltype.Usage
	array, ranged, multi *sqltype.Descriptor
	leaves []sqltype.Usage
}

// probeRoutineSignature is type-only. It neither binds expressions nor changes
// parameter inference, declarations, defaults, or published procedure programs.
func (b *Binder) probeRoutineSignature(operands []functions.Argument, candidate *SQLCallable) bool {
	var families [3]pseudoBinding
	declared := candidate.Procedure.SQL
	var usageStorage [8]sqltype.Usage
	usages := usageStorage[:min(len(candidate.Inputs), len(usageStorage))]
	if len(candidate.Inputs) > len(usageStorage) { usages = make([]sqltype.Usage, len(candidate.Inputs)) }
	for i := range operands { usages[i] = operands[i].Usage }
	if declared == nil {
		declared = &proc.Definition{Inputs: candidate.Inputs, Result: b.Type(sqltype.ID(candidate.Procedure.Result)).Usage()}
	}
	var commonStorage [8]sqltype.Usage
	families[2].leaves = commonStorage[:0]
	if capacity := len(operands)+len(candidate.InputMap); capacity > len(commonStorage) {
		families[2].leaves = make([]sqltype.Usage, 0, capacity)
	}
	collect := func(formal, actual sqltype.Usage) bool {
		id := formal.Type.ID()
		family := sqltype.PolymorphicFamily(id)
		if family == 0 {
			if id == 2281 { return actual.Type != nil && actual.Type == formal.Type }
			if id == 2249 { return actual.Type == nil || actual.Type.BaseType().IsComposite() || actual.Type.IsDeferredRecord() }
			return actual.Type == nil || b.CanCoerce(actual, formal, sqltype.Implicit)
		}
		if actual.Type == nil || actual.Type.ID() == sqltype.Unknown { return true }
		f := &families[family]
		d := actual.Type
		leaf := actual
		switch id {
		case 2277, 5078:
			d = d.BaseType()
			if !d.IsArray() { return false }
			if family == 1 && f.array != nil && f.array != d { return false }
			f.array, leaf = d, d.Element().Usage()
		case 3831, 5080, 4537, 4538:
			d = d.BaseType()
			if (id == 3831 || id == 5080) && !d.IsRange() || (id == 4537 || id == 4538) && !d.IsMultirange() { return false }
			r := d.RangeType()
			if f.ranged != nil && f.ranged != r { return false }
			f.ranged, f.multi = r, r.MultirangeType()
			facts, ok := r.RangeMetadata()
			if !ok { return false }
			leaf = b.Type(sqltype.ID(facts.Subtype)).Usage()
		case 2776, 5079:
			if d.BaseType().IsArray() { return false }
		case 3500:
			if !d.BaseType().IsEnum() { return false }
		}
		if family == 1 {
			if f.leaf.Type != nil && f.leaf.Type != leaf.Type { return false }
			f.leaf = leaf
		} else { f.leaves = append(f.leaves, leaf) }
		return true
	}
	for i, formal := range candidate.Inputs { if !collect(formal, usages[i]) { return false } }
	for slot, source := range candidate.InputMap {
		if source.Start >= 0 { continue }
		if candidate.Procedure.SQLDefaults == nil { return false }
		node, ok := candidate.Procedure.SQLDefaults.Default(slot)
		if !ok || !collect(declared.Inputs[slot], node.ResultUsage()) { return false }
	}
	common := &families[2]
	common.leaf, _ = sqltype.SelectCommonUsage(len(common.leaves), func(i int) sqltype.Usage { return common.leaves[i] }, b.Type(sqltype.Text).Usage(), func(d *sqltype.Descriptor, target sqltype.ID) bool {
		return catalog.CanCoerce(b.BindingCatalog(), d.Usage(), b.Type(target).Usage(), sqltype.Implicit) == nil
	})
	if len(common.leaves) > 0 && common.leaf.Type == nil { return false }
	for family := 1; family <= 2; family++ {
		f := &families[family]
		if f.ranged != nil && f.leaf.Type != nil {
			facts, _ := f.ranged.RangeMetadata()
			if sqltype.ID(facts.Subtype) != f.leaf.Type.ID() { return false }
		}
	}
	resolve := func(formal sqltype.Usage, actual sqltype.Usage) sqltype.Usage {
		id := formal.Type.ID()
		family := sqltype.PolymorphicFamily(id)
		if family == 0 {
			if id == 2249 {
				if formal.Type.IsComposite() { return formal }
				if actual.Type != nil { return actual }
				return sqltype.DeferredRecord().Usage()
			}
			return formal
		}
		f := &families[family]
		if f.leaf.Type == nil { return sqltype.Usage{} }
		switch id {
		case 2277, 5078:
			array, ok := b.definitionArray(f.leaf.Type)
			if !ok { return sqltype.Usage{} }
			return array.Usage()
		case 3831, 5080:
			if f.ranged == nil { return sqltype.Usage{} }; return f.ranged.Usage()
		case 4537, 4538:
			if f.multi == nil { return sqltype.Usage{} }; return f.multi.Usage()
		default:
			return f.leaf
		}
	}
	if len(candidate.Targets) != len(candidate.Inputs) {
		candidate.Targets = make([]sqltype.Usage, len(candidate.Inputs))
	}
	for i, formal := range candidate.Inputs { candidate.Targets[i] = resolve(formal, usages[i]) }
	if candidate.Procedure.SQL == nil { return true }
	result, err := declared.ResolveResult(func(formal sqltype.Usage) sqltype.Usage {
		return resolve(formal, sqltype.Usage{})
	})
	if err != nil { return false }
	signature := proc.InvocationSignature{Inputs: make([]sqltype.Usage, len(declared.Inputs)), Result: result}
	for slot, source := range candidate.InputMap {
		actual := sqltype.Usage{}
		if source.Start >= 0 { actual = usages[source.Start] } else { node, _ := candidate.Procedure.SQLDefaults.Default(slot); actual = node.ResultUsage() }
		signature.Inputs[slot] = resolve(declared.Inputs[slot], actual)
	}
	candidate.Signature = signature
	return true
}
