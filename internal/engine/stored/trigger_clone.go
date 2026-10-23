package stored

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// CloneTrigger installs a partition-owned definition, retaining the exact parent
// definition identity rather than searching parent names at firing time.
func CloneTrigger(read catalog.Read, t Trigger, parent, child *catalog.Relation, oid ids.OID, procedure catalog.ProcedureRemapper) (Trigger, error) {
	p := t.Parts()
	p.OID = oid
	p.Parent = ids.ObjectAddress{ClassID: 2620, ObjectID: t.OID()}
	p.ParentRevision = parent.Revision()
	ref := func(old ids.ObjectRef) ids.ObjectRef {
		if old.Address.ClassID == ids.RelationClassID && old.Address.ObjectID == parent.Address().ObjectID {
			attribute := old.Address.SubID
			old = read.Owner().Domain.Ref(read.Epoch(), child.Address())
			if attribute > 0 {
				for i, a := range parent.Attributes {
					if a == attribute {
						old.Address.SubID = child.Attributes[child.Index[parent.Columns[i].Name]]
						break
					}
				}
			}
		}
		return old
	}
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old != nil && old.ID() == parent.RowType {
			next, _ := read.LookupType(child.RowType)
			return next
		}
		return old
	}
	slots := make(map[int32]int, len(parent.Attributes))
	for i := range parent.Attributes {
		slot, ok := child.Index[parent.Columns[i].Name]
		if !ok {
			return Trigger{}, fmt.Errorf("stuntgres: partition trigger missing attribute")
		}
		slots[child.Attributes[slot]] = slot
	}
	p.Relation = ref(p.Relation)
	p.UpdateColumns = slices.Clone(p.UpdateColumns)
	for i, a := range p.UpdateColumns {
		p.UpdateColumns[i] = ref(ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: parent.Address().ObjectID, SubID: a}}).Address.SubID
	}
	p.References = slices.Clone(p.References)
	for i, a := range p.References {
		p.References[i] = ref(ids.ObjectRef{Address: a}).Address
	}
	if p.When != nil {
		var err error
		p.When, err = RemapExpression(p.When, ref, slots)
		if err != nil {
			return Trigger{}, err
		}
		mapper := &ViewQueryRemapper{Catalog: read, Type: typ, Procedure: procedure}
		p.When, _, err = mapper.retag(p.When, typ)
		if err != nil {
			return Trigger{}, err
		}
		mapping := map[ids.ObjectAddress]ids.ObjectAddress{parent.Address(): child.Address()}
		p.Presentation, err = RemapView(read, p.Presentation, mapping, ref, typ, nil, procedure)
		if err != nil {
			return Trigger{}, err
		}
		binding, err := RemapRuleBinding(read, *p.Binding, ref, typ, nil, procedure)
		if err != nil {
			return Trigger{}, err
		}
		p.Binding = &binding
	}
	return NewTrigger(p)
}
