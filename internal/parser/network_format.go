package parser

import "strings"

// NetworkFormatFunctionName admits ordinary unreserved selected calls. TEXT
// retains the existing function-style cast syntax and nonnetwork conversions.
func NetworkFormatFunctionName(name string) bool {
	return strings.EqualFold(name, "abbrev") || strings.EqualFold(name, "host") || strings.EqualFold(name, "family") || strings.EqualFold(name, "masklen") || strings.EqualFold(name, "inet_same_family") || strings.EqualFold(name, "broadcast")
}
