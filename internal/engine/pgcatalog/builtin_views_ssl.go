package pgcatalog

var sslViewDefinitionParts = sslViewParts()

// Display/dependency metadata only. Native execution projects authorized physical
// sessions without inventing a client_port or a second backing SQL interface.
func sslViewParts() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	fields := [...]string{"pid", "ssl", "sslversion", "sslcipher", "sslbits", "ssl_client_dn", "ssl_client_serial", "ssl_issuer_dn"}
	for i, field := range fields {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewIdentifier(field))
		if field != sslColumns[i].Name {
			parts = append(parts, viewText(" AS "), viewIdentifier(sslColumns[i].Name))
		}
	}
	// Resolve the actual type identity, while retaining the released SQL alias
	// in canonical display text. "integer" is not the catalog lookup name.
	integer := viewType("int4")
	integer.text = "integer"
	parts = append(parts, viewText("\n   FROM "), viewRoutine("pg_stat_get_activity", "23"), viewText("(NULL::"), integer, viewText(") "), viewIdentifier("s"), viewText("("))
	// The released SRF deparse names every OUT attribute, not just this view's
	// eight outputs. These identifiers confer no additional execution interface.
	outputs := [...]string{"datid", "pid", "usesysid", "application_name", "state", "query", "wait_event_type", "wait_event", "xact_start", "query_start", "backend_start", "state_change", "client_addr", "client_hostname", "client_port", "backend_xid", "backend_xmin", "backend_type", "ssl", "sslversion", "sslcipher", "sslbits", "ssl_client_dn", "ssl_client_serial", "ssl_issuer_dn", "gss_auth", "gss_princ", "gss_enc", "gss_delegation", "leader_pid", "query_id"}
	for i, name := range outputs {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(name))
	}
	return append(parts, viewText(")\n  WHERE ("), viewIdentifier("client_port"), viewText(" IS NOT NULL);"))
}
