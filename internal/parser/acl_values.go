package parser

import "strings"

func ACLValueFunctionName(name string) bool {
	return strings.EqualFold(name, "acldefault") || strings.EqualFold(name, "makeaclitem") || strings.EqualFold(name, "aclexplode")
}
