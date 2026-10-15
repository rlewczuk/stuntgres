package parser

import "strings"

// RangeInspectFunctionName admits only the selected inspection/hull overloads;
// preserving spelling lets resolution reject quoted uppercase calls normally.
func RangeInspectFunctionName(name string) bool {
	switch strings.ToLower(name) {
	case "lower", "upper", "isempty", "lower_inc", "upper_inc", "lower_inf", "upper_inf", "range_merge", "multirange":
		return true
	}
	return false
}
