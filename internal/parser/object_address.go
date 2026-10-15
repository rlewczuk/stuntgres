package parser

import "strings"

// Quoted case variants reach ordinary signature resolution rather than syntax
// rejection; only their exact released lowercase names resolve successfully.
func ObjectAddressFunctionName(name string) bool {
	return strings.EqualFold(name, "pg_identify_object") || strings.EqualFold(name, "pg_identify_object_as_address") || strings.EqualFold(name, "pg_get_object_address") || strings.EqualFold(name, "pg_describe_object")
}
