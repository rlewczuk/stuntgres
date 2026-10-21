package regobj

import "stuntgres/internal/sqltype"

// ContainsEmptyACL reports whether a value holds an empty aclitem[] anywhere,
// including inside composites and arrays.
func ContainsEmptyACL(value sqltype.Value) bool {
	if value.Type() == nil || value.IsNull() || !value.Type().ContainsACLItem() {
		return false
	}
	physical := value.Type().BaseType()
	if physical.ID() == sqltype.ACLItemArray && value.ArrayRank() == 1 && value.ArrayDimension(0) == (sqltype.ArrayDimension{Lower: 1, Length: 0}) {
		return true
	}
	if physical.IsComposite() {
		for i := range value.CompositeLen() {
			if ContainsEmptyACL(value.CompositeField(i)) {
				return true
			}
		}
	}
	if physical.IsArray() {
		for i := range value.ArrayLen() {
			if ContainsEmptyACL(value.ArrayElement(i)) {
				return true
			}
		}
	}
	return false
}
