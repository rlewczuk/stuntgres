package pgcatalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Native identities, independent of PostgreSQL's initdb allocation sequence.
const ProgressClusterOID ids.OID = 12208
const ProgressClusterRuleOID ids.OID = 12209

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
	// Simulated CLUSTER never starts a physical rewrite worker.
	return make([][]sqltype.Value, 0), in.Context.Err()
}

func clusterProgressViewParts() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT s.pid, s.datid, d.datname, s.relid, CASE s.param1 WHEN 1 THEN 'CLUSTER'::"), viewType("text"), viewText(" WHEN 2 THEN 'VACUUM FULL'::"), viewType("text"), viewText(" ELSE NULL::"), viewType("text"), viewText(" END AS command, CASE s.param2")}
	for i, phase := range []string{"initializing", "seq scanning heap", "index scanning heap", "sorting tuples", "writing new heap", "swapping relation files", "rebuilding index", "performing final cleanup"} {
		parts = append(parts, viewText(" WHEN "+string(rune('0'+i))+" THEN '"+phase+"'::"), viewType("text"))
	}
	return append(parts, viewText(" ELSE NULL::"), viewType("text"), viewText(" END AS phase, s.param3::"), viewType("oid"), viewText(" AS cluster_index_relid, s.param4 AS heap_tuples_scanned, s.param5 AS heap_tuples_written, s.param6 AS heap_blks_total, s.param7 AS heap_blks_scanned, s.param8 AS index_rebuild_count FROM "), viewRoutine("pg_stat_get_progress_info", "25"), viewText("('CLUSTER'::"), viewType("text"), viewText(") s(pid,datid,relid,param1,param2,param3,param4,param5,param6,param7,param8,param9,param10,param11,param12,param13,param14,param15,param16,param17,param18,param19,param20) LEFT JOIN "), viewClass("pg_database"), viewText(" d ON s.datid=d.oid;"))
}
