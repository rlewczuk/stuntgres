package parser

import "strings"

// AccessInquiryFunctionName also routes quoted case variants to signature errors.
func AccessInquiryFunctionName(name string) bool {
	return strings.EqualFold(name, "pg_has_role") || strings.EqualFold(name, "row_security_active") || strings.EqualFold(name, "pg_get_acl") || strings.EqualFold(name, "has_function_privilege") || strings.EqualFold(name, "has_language_privilege") || strings.EqualFold(name, "has_foreign_data_wrapper_privilege") || strings.EqualFold(name, "has_server_privilege")
}
