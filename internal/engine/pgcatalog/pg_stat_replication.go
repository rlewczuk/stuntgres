package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// StatReplicationOID is the native relation identity, not an initdb view OID.
const StatReplicationOID ids.OID = 12231

var replicationColumns = []ColumnSpec{
	{"pid", sqltype.Int4, true, 0, 0, -1},
	{"usesysid", sqltype.Oid, true, 0, 0, -1},
	{"usename", sqltype.Name, true, 950, 0, -1},
	{"application_name", sqltype.Text, true, 0, 0, -1},
	{"client_addr", sqltype.Inet, true, 0, 0, -1},
	{"client_hostname", sqltype.Text, true, 0, 0, -1},
	{"client_port", sqltype.Int4, true, 0, 0, -1},
	{"backend_start", sqltype.Timestamptz, true, 0, 0, -1},
	{"backend_xmin", sqltype.Xid, true, 0, 0, -1},
	{"state", sqltype.Text, true, 0, 0, -1},
	{"sent_lsn", sqltype.PgLSN, true, 0, 0, -1},
	{"write_lsn", sqltype.PgLSN, true, 0, 0, -1},
	{"flush_lsn", sqltype.PgLSN, true, 0, 0, -1},
	{"replay_lsn", sqltype.PgLSN, true, 0, 0, -1},
	{"write_lag", sqltype.Interval, true, 0, 0, -1},
	{"flush_lag", sqltype.Interval, true, 0, 0, -1},
	{"replay_lag", sqltype.Interval, true, 0, 0, -1},
	{"sync_priority", sqltype.Int4, true, 0, 0, -1},
	{"sync_state", sqltype.Text, true, 0, 0, -1},
	{"reply_time", sqltype.Timestamptz, true, 0, 0, -1},
}

func capturePGStatReplication(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, replicationColumns); err != nil {
		return nil, err
	}
	if !RegisteredSource(in.Catalog, in.Source) || in.Source.Address().ObjectID != StatReplicationOID {
		return nil, fmt.Errorf("stuntgres: invalid replication statistics source")
	}
	// The shared provider has no actual WAL senders. Ordinary client activity,
	// inactive slots and replication configuration cannot supply sender rows.
	// Nonnil emptiness preserves the declared anonymous SQL relation.
	return [][]sqltype.Value{}, nil
}

var replicationViewDefinitionParts = replicationViewParts()

// Display only: the release activity/sender join does not install SQL-callable
// backing functions or introduce a second execution path.
func replicationViewParts() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range replicationColumns {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		alias, name := "s", column.Name
		if i == 2 {
			alias, name = "u", "rolname"
		} else if i >= 9 {
			alias = "w"
		}
		parts = append(parts, viewIdentifier(alias), viewText("."), viewIdentifier(name))
		if i == 2 {
			parts = append(parts, viewText(" AS "), viewIdentifier("usename"))
		}
	}
	// Builtin type syntax remains literal; only object names and identifiers
	// pass through the catalog-aware spelling callback.
	parts = append(parts, viewText("\n   FROM (("), viewRoutine("pg_stat_get_activity", "23"), viewText("(NULL::integer) "), viewIdentifier("s"), viewText("("))
	for i, name := range []string{
		"datid", "pid", "usesysid", "application_name", "state", "query", "wait_event_type", "wait_event",
		"xact_start", "query_start", "backend_start", "state_change", "client_addr", "client_hostname", "client_port",
		"backend_xid", "backend_xmin", "backend_type", "ssl", "sslversion", "sslcipher", "sslbits", "ssl_client_dn",
		"ssl_client_serial", "ssl_issuer_dn", "gss_auth", "gss_princ", "gss_enc", "gss_delegation", "leader_pid", "query_id",
	} {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(name))
	}
	parts = append(parts, viewText(")\n     JOIN "), viewRoutine("pg_stat_get_wal_senders", ""), viewText("() "), viewIdentifier("w"), viewText("("))
	for i, name := range []string{
		"pid", "state", "sent_lsn", "write_lsn", "flush_lsn", "replay_lsn",
		"write_lag", "flush_lag", "replay_lag", "sync_priority", "sync_state", "reply_time",
	} {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(name))
	}
	return append(parts, viewText(") ON (("), viewIdentifier("s"), viewText("."), viewIdentifier("pid"), viewText(" = "), viewIdentifier("w"), viewText("."), viewIdentifier("pid"), viewText(")))\n     LEFT JOIN "), viewClass("pg_authid"), viewText(" "), viewIdentifier("u"), viewText(" ON (("), viewIdentifier("s"), viewText("."), viewIdentifier("usesysid"), viewText(" = "), viewIdentifier("u"), viewText("."), viewIdentifier("oid"), viewText(")));"))
}
