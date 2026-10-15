package parser

import "strings"

// NamespaceInquiryFunctionName admits only the selected builtins. Quoted case
// variants reach ordinary missing-signature diagnostics without folding identity.
func NamespaceInquiryFunctionName(name string) bool {
	return strings.EqualFold(name, "pg_my_temp_schema") || strings.EqualFold(name, "pg_is_other_temp_schema")
}
