package sqltype

import "sync"

// builtinTemplate is one builtin generation built from the compiled tables,
// with each descriptor's position. Builtins copies it: every descriptor
// pointer of a builtin points into its own generation. The only composite
// definition is the fixed NULL-only pg_statistic description, copied below.
type builtinTemplate struct {
	types    []*Descriptor
	position map[*Descriptor]int
}

var builtins = sync.OnceValue(func() builtinTemplate {
	t := builtinTemplate{types: buildBuiltins()}
	t.position = make(map[*Descriptor]int, len(t.types))
	for i, d := range t.types {
		if d.enum != nil || d.composite != nil && d.id != 10029 || d.customRange != nil {
			panic("stuntgres: builtin type template holds a definition it cannot copy")
		}
		t.position[d] = i
	}
	return t
})

// cloneBuiltins returns a fresh generation equal to buildBuiltins(): one copy
// of each template descriptor whose descriptor pointers are moved to the
// copies.
func cloneBuiltins() []*Descriptor {
	t := builtins()
	copies := make([]Descriptor, len(t.types))
	out := make([]*Descriptor, len(t.types))
	for i, d := range t.types {
		copies[i] = *d
		out[i] = &copies[i]
		if d.composite != nil {
			definition := &compositeDefinition{members: append([]CompositeMember(nil), d.composite.members...), attributes: d.composite.attributes}
			copies[i].composite = definition
		}
	}
	move := func(p **Descriptor) {
		if *p != nil {
			i, ok := t.position[*p]
			if !ok {
				panic("stuntgres: builtin type points outside its generation")
			}
			*p = &copies[i]
		}
	}
	for i := range copies {
		for _, p := range copies[i].descriptorPointers() {
			move(p)
		}
		if copies[i].composite != nil {
			for j := range copies[i].composite.members {
				move(&copies[i].composite.members[j].Usage.Type)
			}
		}
	}
	return out
}

// descriptorPointers lists d's fields that point to descriptors of the same
// generation. A new such field must be added here; TestBuiltinsCopyTemplate
// checks that a copy points only into itself.
func (d *Descriptor) descriptorPointers() [27]**Descriptor {
	return [27]**Descriptor{
		&d.base.Type, &d.element, &d.rangeSubtype, &d.rangeBoolean, &d.multirangeRange,
		&d.multirangeCompanion, &d.networkCIDR, &d.networkMasklen, &d.networkBoolean,
		&d.geometryPoint, &d.circleBoolean, &d.circleFloat, &d.pointBoolean, &d.pointFloat,
		&d.polygonBoolean, &d.polygonPoint, &d.polygonInt, &d.pathInt, &d.ftsBoolean,
		&d.ftsDistance, &d.ftsText, &d.ftsChar, &d.ftsTextArray, &d.ftsCharArray,
		&d.catalogNullOwner, &d.queryNullOwner, &d.polymorphicOwner,
	}
}
