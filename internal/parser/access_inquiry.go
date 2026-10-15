package parser

import "strings"

// AccessInquiryFunctionName also routes quoted case variants to signature errors.
func AccessInquiryFunctionName(name string) bool {
	return strings.EqualFold(name, "pg_has_role") || strings.EqualFold(name, "row_security_active")
}
