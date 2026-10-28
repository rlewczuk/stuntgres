package parser

import "strings"

// These calls share the ordinary selected scalar/record function lanes. age(xid)
// remains in the existing temporal family's overload resolver, including UNKNOWN.
func TransactionIdentityFunctionName(name string) bool {
	switch strings.ToLower(name) {
	case "mxid_age", "pg_current_xact_id", "pg_current_xact_id_if_assigned", "pg_xact_status", "pg_get_multixact_members",
		"txid_current", "txid_current_if_assigned", "txid_status":
		return true
	}
	return false
}
