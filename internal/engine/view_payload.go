package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// viewOf returns d's concrete view definition, or nil when d stores none.
func viewOf(d *catalog.Relation) *viewDefinition {
	v, _ := d.View.(*viewDefinition)
	return v
}

// viewPayloadOf stores v as a payload; a nil definition stays a nil payload,
// so d.view == nil keeps meaning "no stored definition".
func viewPayloadOf(v *viewDefinition) catalog.ViewPayload {
	if v == nil {
		return nil
	}
	return v
}

func (v *viewDefinition) RuleOID() ids.OID { return v.rule }

func (v *viewDefinition) DependsOn(target ids.ObjectAddress) bool {
	for _, leaf := range v.sources {
		if target.SubID == 0 && leaf.ref.Address == target {
			return true
		}
	}
	for _, dependency := range v.dependencies {
		a := dependency.Ref.Address
		if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
			return true
		}
	}
	return false
}

func (v *viewDefinition) TemporaryConsumer() (ids.ObjectAddress, ids.SessionToken) {
	return v.consumer, v.temporary
}

func (v *viewDefinition) CapturedReference(ref ids.ObjectRef) (catalog.Descriptor, bool) {
	d, ok := v.references[ref]
	return d, ok
}
