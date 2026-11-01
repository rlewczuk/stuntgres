package pgcatalog

import (
	"strconv"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Native identities, independent of PostgreSQL's initdb allocation sequence.
const ProgressClusterOID ids.OID = 12208
const ProgressClusterRuleOID ids.OID = 12209

// ClusterProgressFact describes one genuine FULL worker. Simulated CLUSTER
// never registers a physical rewrite worker.
type ClusterProgressFact struct {
	PID                       int64
	Database                  ids.OID
	DatabaseName              string
	Relation                  ids.OID
	Phase                     string
	Scanned, Written, Indexes int64
	Visible                   bool
}

var progressClusterColumns = []ColumnSpec{
	{"pid", sqltype.Int4, true, 0, 0, -1},
	{"datid", sqltype.Oid, true, 0, 0, -1},
	{"datname", sqltype.Name, true, 0, 0, -1},
	{"relid", sqltype.Oid, true, 0, 0, -1},
	{"command", sqltype.Text, true, 950, 0, -1},
	{"phase", sqltype.Text, true, 950, 0, -1},
	{"cluster_index_relid", sqltype.Oid, true, 0, 0, -1},
	{"heap_tuples_scanned", sqltype.Int8, true, 0, 0, -1},
	{"heap_tuples_written", sqltype.Int8, true, 0, 0, -1},
	{"heap_blks_total", sqltype.Int8, true, 0, 0, -1},
	{"heap_blks_scanned", sqltype.Int8, true, 0, 0, -1},
	{"index_rebuild_count", sqltype.Int8, true, 0, 0, -1},
}

func captureClusterProgress(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, progressClusterColumns); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	err := in.Runtime.VisitClusterProgress(in.Context, func(f ClusterProgressFact) error {
		if err := in.Context.Err(); err != nil {
			return err
		}
		row := make([]sqltype.Value, len(progressClusterColumns))
		for i := range row {
			row[i] = sqltype.Null(in.Source.Columns[i].Usage.Type)
		}
		var err error
		row[0], err = sqltype.Integer(in.Source.Columns[0].Usage.Type, f.PID)
		if err != nil {
			return err
		}
		row[1], err = sqltype.OIDValue(in.Source.Columns[1].Usage.Type, uint32(f.Database))
		if err != nil {
			return err
		}
		row[2] = sqltype.String(in.Source.Columns[2].Usage.Type, f.DatabaseName)
		if f.Visible {
			row[3], err = sqltype.OIDValue(in.Source.Columns[3].Usage.Type, uint32(f.Relation))
			if err != nil {
				return err
			}
			row[4] = sqltype.String(in.Source.Columns[4].Usage.Type, "VACUUM FULL")
			row[5] = sqltype.String(in.Source.Columns[5].Usage.Type, f.Phase)
			row[6], err = sqltype.OIDValue(in.Source.Columns[6].Usage.Type, 0)
			if err != nil {
				return err
			}
			for i, value := range [...]int64{f.Scanned, f.Written, 0, 0, f.Indexes} {
				row[i+7], err = sqltype.Integer(in.Source.Columns[i+7].Usage.Type, value)
				if err != nil {
					return err
				}
			}
		}
		rows = append(rows, row)
		return nil
	})
	return rows, err
}

// REL_18_6 src/backend/catalog/system_views.sql pg_stat_progress_cluster.
// These are structural display fragments, not execution of the released view
// or authority to manufacture CLUSTER workers in the native FULL producer.
func clusterProgressViewParts(pretty bool) []builtinViewPart {
	var parts []builtinViewPart
	text := func(s string) { parts = append(parts, viewText(s)) }
	ident := func(s string) { parts = append(parts, viewIdentifier(s)) }
	field := func(alias, name string) { ident(alias); text("."); ident(name) }
	text(" SELECT ")
	field("s", "pid")
	text(",\n    ")
	field("s", "datid")
	text(",\n    ")
	field("d", "datname")
	text(",\n    ")
	field("s", "relid")
	text(",\n        CASE ")
	field("s", "param1")
	text("\n")
	for i, label := range []string{"CLUSTER", "VACUUM FULL"} {
		text("            WHEN " + strconv.Itoa(i+1) + " THEN '" + label + "'::")
		parts = append(parts, viewType("text"))
		text("\n")
	}
	text("            ELSE NULL::")
	parts = append(parts, viewType("text"))
	text("\n        END AS ")
	ident("command")
	text(",\n        CASE ")
	field("s", "param2")
	text("\n")
	for i, label := range []string{"initializing", "seq scanning heap", "index scanning heap", "sorting tuples", "writing new heap", "swapping relation files", "rebuilding index", "performing final cleanup"} {
		text("            WHEN " + strconv.Itoa(i) + " THEN '" + label + "'::")
		parts = append(parts, viewType("text"))
		text("\n")
	}
	text("            ELSE NULL::")
	parts = append(parts, viewType("text"))
	text("\n        END AS ")
	ident("phase")
	text(",\n    ")
	if !pretty {
		text("(")
	}
	field("s", "param3")
	if !pretty {
		text(")")
	}
	text("::")
	parts = append(parts, viewType("oid"))
	text(" AS ")
	ident("cluster_index_relid")
	for i, name := range []string{"heap_tuples_scanned", "heap_tuples_written", "heap_blks_total", "heap_blks_scanned", "index_rebuild_count"} {
		text(",\n    ")
		field("s", "param"+strconv.Itoa(i+4))
		text(" AS ")
		ident(name)
	}
	text("\n   FROM ")
	if !pretty {
		text("(")
	}
	parts = append(parts, viewRoutine("pg_stat_get_progress_info", "25"))
	text("('CLUSTER'::")
	parts = append(parts, viewType("text"))
	text(") ")
	ident("s")
	text("(")
	for i, name := range append([]string{"pid", "datid", "relid"}, progressParameterNames()...) {
		if i > 0 {
			text(", ")
		}
		ident(name)
	}
	text(")\n     LEFT JOIN ")
	parts = append(parts, viewClass("pg_database"))
	text(" ")
	ident("d")
	text(" ON ")
	if !pretty {
		text("((")
	}
	field("s", "datid")
	text(" = ")
	field("d", "oid")
	if !pretty {
		text(")))")
	}
	text(";")
	return parts
}
