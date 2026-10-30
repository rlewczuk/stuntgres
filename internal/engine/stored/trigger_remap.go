package stored

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// RemapTriggers maps each identity and type exactly once into the destination
// catalog, leaving every source descriptor, recipe and frame untouched.
func RemapTriggers(read catalog.Read, d *catalog.Relation, mapping map[ids.ObjectAddress]ids.ObjectAddress, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collation typedef.CollationRemapper, procedure catalog.ProcedureRemapper) (*catalog.Relation, error) {
	return remapTriggers(read, d, mapping, ref, typ, collation, procedure, nil)
}

func remapTriggers(read catalog.Read, d *catalog.Relation, mapping map[ids.ObjectAddress]ids.ObjectAddress, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collation typedef.CollationRemapper, procedure catalog.ProcedureRemapper, refresh dependencyRefresh) (*catalog.Relation, error) {
	if !d.HasTriggers() {
		return d, nil
	}
	copy := *d
	list := slices.Clone(TriggersOf(d))
	for i, t := range list {
		p := t.Parts()
		p.Relation, p.Procedure = ref(p.Relation), ref(p.Procedure)
		if p.Parent.ObjectID != 0 {
			p.Parent = ref(ids.ObjectRef{Address: p.Parent}).Address
		}
		p.References = slices.Clone(p.References)
		for j, a := range p.References {
			p.References[j] = ref(ids.ObjectRef{Address: a}).Address
		}
		p.AutoExtensions = slices.Clone(p.AutoExtensions)
		for j, a := range p.AutoExtensions {
			p.AutoExtensions[j] = ref(ids.ObjectRef{Address: a}).Address
			ext, ok := read.Extension(p.AutoExtensions[j])
			if !ok {
				return nil, ids.ErrCatalogReplaced
			}
			if err := extension.ValidateProviderHeader(read, ext, ids.ExtensionClassID); err != nil {
				return nil, err
			}
		}
		if p.When != nil {
			var err error
			slots := make(map[int32]int, len(d.Attributes))
			for slot, attribute := range d.Attributes {
				slots[attribute] = slot
			}
			p.When, err = RemapExpression(p.When, ref, slots)
			if err != nil {
				return nil, err
			}
			mapper := &ViewQueryRemapper{Catalog: read, Type: typ, Collation: collation, Procedure: procedure}
			p.When, _, err = mapper.retag(p.When, typ)
			if err != nil {
				return nil, err
			}
			p.Presentation, err = remapView(read, p.Presentation, mapping, ref, typ, collation, nil, procedure, refresh)
			if err != nil {
				return nil, err
			}
			binding, err := remapRuleBinding(read, *p.Binding, ref, typ, collation, 0, nil, procedure, refresh)
			if err != nil {
				return nil, err
			}
			p.Binding = &binding
		}
		var err error
		list[i], err = NewTrigger(p)
		if err != nil {
			return nil, err
		}
	}
	copy.Triggers = TriggersPayload(list)
	return &copy, nil
}
