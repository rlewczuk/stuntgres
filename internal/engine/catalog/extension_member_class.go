package catalog

import "stuntgres/internal/engine/ids"

// ExtensionMemberEligible classifies represented member descriptors without
// allocating a replacement or conferring command-specific ownership authority.
func ExtensionMemberEligible(d Descriptor) bool {
	switch d := d.(type) {
	case *Type, *Procedure, *Operator, *OperatorClass, *OperatorFamily, *TSParser, *TSTemplate, *TSDictionary, *TSConfiguration, *Namespace, *Relation, *Collation, *Conversion, *Language, *AccessMethod, *EventTrigger, *Cast:
		return true
	case *ForeignObject:
		return d.Address().ClassID == ids.ForeignWrapperClassID || d.Address().ClassID == ids.ForeignServerClassID
	}
	return false
}
