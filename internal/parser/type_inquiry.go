package parser

import "strings"

// TypeInquiryFunctionName admits only the selected scalar family; normal
// function resolution still owns exact spelling and namespace visibility.
func TypeInquiryFunctionName(name string) bool {
	return strings.EqualFold(name, "format_type") || strings.EqualFold(name, "pg_basetype") || strings.EqualFold(name, "pg_typeof")
}
