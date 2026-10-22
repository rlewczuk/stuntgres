package index

import "stuntgres/internal/engine/catalog"

// MethodProperty operates only after a real captured descriptor selected the
// index implementation. Missing and TABLE methods never reach this function.
func MethodProperty(method catalog.IndexMethod, property string) (bool, bool) {
	var order, unique, multi, exclude, include bool
	switch method {
	case catalog.BtreeMethod:
		order, unique, multi, exclude, include = true, true, true, true, true
	case catalog.HashMethod:
		exclude = true
	case catalog.GistMethod:
		multi, exclude, include = true, true, true
	default:
		return false, false
	}
	switch property {
	case "can_order":
		return order, true
	case "can_unique":
		return unique, true
	case "can_multi_col":
		return multi, true
	case "can_exclude":
		return exclude, true
	case "can_include":
		return include, true
	}
	return false, false
}
