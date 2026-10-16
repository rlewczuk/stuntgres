package parser

import "strings"

// These calls share the ordinary selected scalar/record function lanes. age(xid)
// remains in the existing temporal family's overload resolver, including UNKNOWN.
func TransactionIdentityFunctionName(name string) bool {
	return strings.EqualFold(name, "mxid_age") || strings.EqualFold(name, "pg_current_xact_id") || strings.EqualFold(name, "pg_current_xact_id_if_assigned") || strings.EqualFold(name, "pg_xact_status") || strings.EqualFold(name, "pg_get_multixact_members")
}
