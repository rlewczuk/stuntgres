package parser

import "strings"

// UUIDExtractFunctionName admits the selected scalar syntax without reserving
// either spelling as a keyword. Quoted case is retained for binder resolution.
func UUIDExtractFunctionName(name string) bool {
	return strings.EqualFold(name, "uuid_extract_timestamp") || strings.EqualFold(name, "uuid_extract_version")
}
