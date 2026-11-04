package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// StatWALReceiverOID is the native relation identity, not an initdb view OID.
const StatWALReceiverOID ids.OID = 12240

var walReceiverColumns = []ColumnSpec{
	{"pid", sqltype.Int4, true, 0, 0, -1},
	{"status", sqltype.Text, true, 0, 0, -1},
	{"receive_start_lsn", sqltype.PgLSN, true, 0, 0, -1},
	{"receive_start_tli", sqltype.Int4, true, 0, 0, -1},
	{"written_lsn", sqltype.PgLSN, true, 0, 0, -1},
	{"flushed_lsn", sqltype.PgLSN, true, 0, 0, -1},
	{"received_tli", sqltype.Int4, true, 0, 0, -1},
	{"last_msg_send_time", sqltype.Timestamptz, true, 0, 0, -1},
	{"last_msg_receipt_time", sqltype.Timestamptz, true, 0, 0, -1},
	{"latest_end_lsn", sqltype.PgLSN, true, 0, 0, -1},
	{"latest_end_time", sqltype.Timestamptz, true, 0, 0, -1},
	{"slot_name", sqltype.Text, true, 0, 0, -1},
	{"sender_host", sqltype.Text, true, 0, 0, -1},
	{"sender_port", sqltype.Int4, true, 0, 0, -1},
	{"conninfo", sqltype.Text, true, 0, 0, -1},
}

func capturePGStatWALReceiver(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, walReceiverColumns); err != nil {
		return nil, err
	}
	if !RegisteredSource(in.Catalog, in.Source) || in.Source.Address().ObjectID != StatWALReceiverOID {
		return nil, fmt.Errorf("stuntgres: invalid WAL receiver statistics source")
	}
	// There is no actual display-ready physical receiver in the shared provider.
	// Ordinary sessions, slots and configuration cannot establish a receiver row.
	// A future actual-row path must expose only pid without superuser/effective
	// pg_read_all_stats authority: there is no own-role exception. Meaningless
	// fields stay NULL, and actual conninfo must conceal sensitive options even
	// for privileged readers. Empty capture needs no unused masking machinery.
	return [][]sqltype.Value{}, nil
}

// Display only: this does not install an executable backing function or a
// second capture route. The release view filters the absent function result.
func walReceiverViewParts(pretty bool) []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range walReceiverColumns {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	parts = append(parts, viewText("\n   FROM "), viewRoutine("pg_stat_get_wal_receiver", ""), viewText("() "), viewIdentifier("s"), viewText("("))
	for i, column := range walReceiverColumns {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	if pretty {
		return append(parts, viewText(")\n  WHERE "), viewIdentifier("pid"), viewText(" IS NOT NULL;"))
	}
	return append(parts, viewText(")\n  WHERE ("), viewIdentifier("pid"), viewText(" IS NOT NULL);"))
}
