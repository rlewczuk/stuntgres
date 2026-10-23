package reference_test

import "strings"

func icMaintenanceInventory(inventory map[string][]string) {
	stats := strings.Fields("relid schemaname relname seq_scan last_seq_scan seq_tup_read idx_scan last_idx_scan idx_tup_fetch n_tup_ins n_tup_upd n_tup_del n_tup_hot_upd n_tup_newpage_upd n_live_tup n_dead_tup n_mod_since_analyze n_ins_since_vacuum last_vacuum last_autovacuum last_analyze last_autoanalyze vacuum_count autovacuum_count analyze_count autoanalyze_count total_vacuum_time total_autovacuum_time total_analyze_time total_autoanalyze_time")
	for _, name := range []string{"pg_stat_all_tables", "pg_stat_sys_tables", "pg_stat_user_tables"} {
		inventory[name] = stats
	}
	inventory["pg_stat_progress_vacuum"] = strings.Fields("pid datid datname relid phase heap_blks_total heap_blks_scanned heap_blks_vacuumed index_vacuum_count max_dead_tuple_bytes dead_tuple_bytes num_dead_item_ids indexes_total indexes_processed delay_time")
}
