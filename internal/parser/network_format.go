package parser

import "strings"

// NetworkFormatFunctionName admits ordinary unreserved selected calls. TEXT
// retains the existing function-style cast syntax and nonnetwork conversions.
func NetworkFormatFunctionName(name string) bool {
	return strings.EqualFold(name, "abbrev") || strings.EqualFold(name, "host") || strings.EqualFold(name, "family") || strings.EqualFold(name, "masklen") || strings.EqualFold(name, "inet_same_family") || strings.EqualFold(name, "broadcast") || strings.EqualFold(name, "hostmask") || strings.EqualFold(name, "netmask") || strings.EqualFold(name, "network") || strings.EqualFold(name, "inet_merge") || strings.EqualFold(name, "set_masklen")
}
